from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import json
import logging
import os
import signal
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse
from typing import Iterable
DEFAULT_REPO_DIR = Path(os.environ.get("OUTLOOK_REGISTER_DIR", "/opt/OutlookRegister"))
DEFAULT_RESULTS_DIR = DEFAULT_REPO_DIR / "Results"
DEFAULT_OAUTH_CLIENT_ID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
DEFAULT_OAUTH_REDIRECT_URL = "https://login.microsoftonline.com/common/oauth2/nativeclient"
DEFAULT_SCOPES = "offline_access https://graph.microsoft.com/Mail.Read"
LOCK_FILE_NAME = ".nb_register.lock"
PROXY_POOL_INDEX_FILE_NAME = ".proxy_pool_index"
PROXY_POOL_LOCK_FILE_NAME = ".proxy_pool.lock"
LAST_REGISTRATION_ERROR_FILE_NAME = "last_registration_error.txt"

logger = logging.getLogger("outlook-imap-register")


@dataclass(frozen=True)
class MailboxRecord:
    email: str
    password: str
    refresh_token: str = ""
    access_token: str = ""
    source: str = ""


class RegistrationAlreadyRunning(RuntimeError):
    pass


def env_bool(name: str, default: bool) -> bool:
    value = os.environ.get(name, "").strip().lower()
    if value == "":
        return default
    return value in {"1", "true", "yes", "y", "on"}


def env_int(name: str, default: int) -> int:
    value = os.environ.get(name, "").strip()
    if value == "":
        return default
    try:
        return int(value)
    except ValueError:
        logger.warning("invalid integer %s=%r; using %s", name, value, default)
        return default


def env_str(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def split_scopes(value: str) -> list[str]:
    value = value.strip()
    if value == "":
        value = DEFAULT_SCOPES
    if value.startswith("["):
        parsed = json.loads(value)
        return [str(item).strip() for item in parsed if str(item).strip()]
    return [part for part in value.replace(",", " ").split() if part]


def repo_dir() -> Path:
    return Path(env_str("OUTLOOK_REGISTER_DIR", str(DEFAULT_REPO_DIR))).resolve()


def results_dir() -> Path:
    return Path(env_str("OUTLOOK_REGISTER_RESULTS_DIR", str(repo_dir() / "Results"))).resolve()


def proxy_pool_entries() -> list[str]:
    entries: list[str] = []
    raw_pool = env_str("OUTLOOK_REGISTER_PROXY_POOL")
    if raw_pool:
        for part in raw_pool.replace(",", "\n").splitlines():
            entries.extend(item.strip() for item in part.split() if item.strip())

    proxy_file = env_str("OUTLOOK_REGISTER_PROXY_FILE")
    if proxy_file:
        path = Path(proxy_file).expanduser()
        if path.exists():
            for raw in path.read_text(encoding="utf-8").splitlines():
                line = raw.strip()
                if line and not line.startswith("#"):
                    entries.append(line)
        else:
            logger.warning("OUTLOOK_REGISTER_PROXY_FILE does not exist: %s", path)

    fallback = env_str("OUTLOOK_REGISTER_PROXY")
    if fallback and not entries:
        entries.append(fallback)

    unique: list[str] = []
    seen: set[str] = set()
    for entry in entries:
        if entry not in seen:
            seen.add(entry)
            unique.append(entry)
    return unique


def redact_proxy(proxy: str) -> str:
    if not proxy:
        return ""
    try:
        parsed = urlparse(proxy)
        if not parsed.scheme or not parsed.hostname:
            return "<proxy>"
        port = f":{parsed.port}" if parsed.port else ""
        return f"{parsed.scheme}://{parsed.hostname}{port}"
    except Exception:
        return "<proxy>"


def next_proxy() -> str:
    entries = proxy_pool_entries()
    if not entries:
        return ""
    if len(entries) == 1:
        return entries[0]

    out_dir = results_dir()
    out_dir.mkdir(parents=True, exist_ok=True)
    lock_path = out_dir / PROXY_POOL_LOCK_FILE_NAME
    index_path = out_dir / PROXY_POOL_INDEX_FILE_NAME
    with lock_path.open("w", encoding="utf-8") as lock_file:
        fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
        try:
            try:
                index = int(index_path.read_text(encoding="utf-8").strip() or "0")
            except (OSError, ValueError):
                index = 0
            selected = entries[index % len(entries)]
            index_path.write_text(str(index + 1) + "\n", encoding="utf-8")
            return selected
        finally:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)


def proxy_env(proxy: str) -> dict[str, str]:
    values: dict[str, str] = {}
    if not proxy:
        return values
    values["OUTLOOK_REGISTER_PROXY"] = proxy
    values["HTTP_PROXY"] = proxy
    values["HTTPS_PROXY"] = proxy
    values["http_proxy"] = proxy
    values["https_proxy"] = proxy
    values.setdefault("NO_PROXY", "localhost,127.0.0.1")
    values.setdefault("no_proxy", "localhost,127.0.0.1")
    return values


def redact_email(email: str) -> str:
    local, sep, domain = email.partition("@")
    if not sep:
        return "***"
    if len(local) > 2:
        local = local[:2] + "***"
    else:
        local = "***"
    return f"{local}@{domain}"


def build_register_config(proxy: str | None = None) -> dict:
    enable_oauth2 = env_bool("OUTLOOK_REGISTER_ENABLE_OAUTH2", True)
    client_id = (
        env_str("OUTLOOK_REGISTER_OAUTH_CLIENT_ID")
        or env_str("OUTLOOK_OAUTH_CLIENT_ID")
        or DEFAULT_OAUTH_CLIENT_ID
    )
    redirect_url = (
        env_str("OUTLOOK_REGISTER_OAUTH_REDIRECT_URL")
        or env_str("OUTLOOK_OAUTH_REDIRECT_URL")
        or DEFAULT_OAUTH_REDIRECT_URL
    )
    if enable_oauth2 and (not client_id or not redirect_url):
        raise SystemExit(
            "OUTLOOK_REGISTER_OAUTH_CLIENT_ID and OUTLOOK_REGISTER_OAUTH_REDIRECT_URL "
            "are required when OUTLOOK_REGISTER_ENABLE_OAUTH2=true"
        )

    return {
        "choose_browser": env_str("OUTLOOK_REGISTER_BROWSER", "patchright"),
        "email_suffix": env_str("OUTLOOK_REGISTER_EMAIL_SUFFIX", "@outlook.com"),
        "proxy": env_str("OUTLOOK_REGISTER_PROXY") if proxy is None else proxy,
        "bot_protection_wait": env_int("OUTLOOK_REGISTER_BOT_PROTECTION_WAIT", 11),
        "max_captcha_retries": env_int("OUTLOOK_REGISTER_MAX_CAPTCHA_RETRIES", 2),
        "manual_captcha": env_bool("OUTLOOK_REGISTER_MANUAL_CAPTCHA", False),
        "manual_captcha_timeout": env_int("OUTLOOK_REGISTER_MANUAL_CAPTCHA_TIMEOUT_SECONDS", 300),
        "concurrent_flows": forced_single_value("OUTLOOK_REGISTER_CONCURRENT_FLOWS"),
        "max_tasks": forced_single_value("OUTLOOK_REGISTER_MAX_TASKS"),
        "oauth2_required": env_bool("OUTLOOK_REGISTER_REQUIRE_OAUTH2", False),
        "oauth2": {
            "enable_oauth2": enable_oauth2,
            "client_id": client_id,
            "redirect_url": redirect_url,
            "Scopes": split_scopes(env_str("OUTLOOK_REGISTER_OAUTH_SCOPES", DEFAULT_SCOPES)),
        },
        "playwright": {
            "browser_path": env_str("OUTLOOK_REGISTER_PLAYWRIGHT_BROWSER_PATH"),
        },
    }


def forced_single_value(name: str) -> int:
    configured = env_int(name, 1)
    if configured != 1:
        logger.warning("%s=%s ignored; OutlookRegister is forced to single-thread mode", name, configured)
    return 1


@contextmanager
def registration_lock():
    default_lock_path = results_dir() / LOCK_FILE_NAME
    lock_path = Path(env_str("OUTLOOK_REGISTER_LOCK_FILE", str(default_lock_path))).resolve()
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    wait_seconds = env_int("OUTLOOK_REGISTER_LOCK_WAIT_SECONDS", 0)
    deadline = time.monotonic() + wait_seconds

    with lock_path.open("w", encoding="utf-8") as lock_file:
        while True:
            try:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError as exc:
                if wait_seconds <= 0 or time.monotonic() >= deadline:
                    raise RegistrationAlreadyRunning(
                        f"OutlookRegister is already running; lock={lock_path}"
                    ) from exc
                time.sleep(1)

        lock_file.seek(0)
        lock_file.truncate()
        lock_file.write(f"pid={os.getpid()}\nstarted_at={int(time.time())}\n")
        lock_file.flush()
        try:
            yield
        finally:
            lock_file.seek(0)
            lock_file.truncate()
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)


def write_register_config(path: Path, proxy: str = "") -> None:
    config_path = path / "config.json"
    config_path.write_text(
        json.dumps(build_register_config(proxy=proxy), ensure_ascii=False, indent=4) + "\n",
        encoding="utf-8",
    )
    logger.info("wrote OutlookRegister config to %s", config_path)


def run_outlook_register(path: Path, proxy: str = "") -> int:
    script_path = Path("/app/camoufox_register.py")
    if not script_path.exists():
        logger.warning(f"Camoufox script not found at {script_path}, looking in current dir")
        script_path = Path("camoufox_register.py")
        if not script_path.exists():
            raise FileNotFoundError("camoufox_register.py not found")

    command = [sys.executable, "-u", str(script_path)]
    if env_bool("OUTLOOK_REGISTER_USE_XVFB", False) and not os.environ.get("DISPLAY"):
        command = ["xvfb-run", "-a", *command]

    if proxy:
        logger.info("starting Camoufox registration with proxy %s", redact_proxy(proxy))
    else:
        logger.info("starting Camoufox registration without proxy")
    timeout = env_int("OUTLOOK_REGISTER_RUN_TIMEOUT_SECONDS", 900)
    env = os.environ.copy()
    env["PYTHONUNBUFFERED"] = "1"
    env.update(proxy_env(proxy))
    process = subprocess.Popen(command, cwd="/app", env=env, start_new_session=True)
    try:
        code = process.wait(timeout=timeout if timeout > 0 else None)
    except subprocess.TimeoutExpired:
        logger.warning("Camoufox script exceeded %ss; terminating browser process group", timeout)
        terminate_process_group(process.pid)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            logger.warning("Camoufox script did not exit cleanly after process group termination")
        return 124

    if code != 0:
        logger.warning("Camoufox script exited with code %s; collecting any completed results", code)
    return code


def last_registration_error(path: Path) -> str:
    error_path = path / LAST_REGISTRATION_ERROR_FILE_NAME
    if not error_path.exists():
        return ""
    try:
        return error_path.read_text(encoding="utf-8").strip()
    except OSError as exc:
        logger.warning("failed to read last registration error %s: %s", error_path, exc)
        return ""


def clear_last_registration_error(path: Path) -> None:
    try:
        (path / LAST_REGISTRATION_ERROR_FILE_NAME).unlink(missing_ok=True)
    except OSError as exc:
        logger.warning("failed to clear last registration error: %s", exc)


def browser_subprocess_env(proxy: str = "") -> dict[str, str]:
    env = os.environ.copy()
    env["PYTHONUNBUFFERED"] = "1"
    env.update(proxy_env(proxy or env_str("OUTLOOK_REGISTER_PROXY")))
    return env


def terminate_process_group(pid: int) -> None:
    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    except OSError as exc:
        logger.warning("failed to terminate OutlookRegister process group: %s", exc)
        return
    for _ in range(10):
        try:
            os.killpg(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.2)
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    except OSError as exc:
        logger.warning("failed to kill OutlookRegister process group: %s", exc)


def parse_token_file(path: Path) -> Iterable[MailboxRecord]:
    if not path.exists():
        return
    for line_no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("---", 4)
        if len(parts) < 3:
            logger.warning("skip malformed token line %s:%s", path, line_no)
            continue
        email, password, refresh_token = (part.strip() for part in parts[:3])
        access_token = parts[3].strip() if len(parts) >= 4 else ""
        if not email or not refresh_token:
            logger.warning("skip token line without email or refresh token %s:%s", path, line_no)
            continue
        yield MailboxRecord(
            email=email.lower(),
            password=password,
            refresh_token=refresh_token,
            access_token=access_token,
            source=path.name,
        )


def parse_password_file(path: Path) -> Iterable[MailboxRecord]:
    if not path.exists():
        return
    for line_no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if ":" not in line:
            logger.warning("skip malformed mailbox line %s:%s", path, line_no)
            continue
        email, password = (part.strip() for part in line.split(":", 1))
        if not email:
            logger.warning("skip mailbox line without email %s:%s", path, line_no)
            continue
        yield MailboxRecord(email=email.lower(), password=password, source=path.name)


def collect_records(path: Path, include_password_only: bool) -> list[MailboxRecord]:
    records: dict[str, MailboxRecord] = {}
    for record in parse_token_file(path / "outlook_token.txt") or []:
        records[record.email] = record

    if include_password_only:
        for filename in ("logged_email.txt", "unlogged_email.txt"):
            for record in parse_password_file(path / filename) or []:
                records.setdefault(record.email, record)

    return list(records.values())


def record_response(record: MailboxRecord) -> dict:
    return {
        "email_address": record.email,
        "password": record.password,
        "refresh_token": record.refresh_token,
        "access_token": record.access_token,
        "source": record.source,
    }


def new_or_updated_records(before: dict[str, MailboxRecord], after: list[MailboxRecord]) -> list[MailboxRecord]:
    changed: list[MailboxRecord] = []
    for record in after:
        old = before.get(record.email)
        if old is None:
            changed.append(record)
            continue
        if record.refresh_token and record.refresh_token != old.refresh_token:
            changed.append(record)
            continue
        if record.access_token and record.access_token != old.access_token:
            changed.append(record)
    return changed


def records_with_oauth_results(records: list[MailboxRecord], oauth_result: dict) -> list[MailboxRecord]:
    token_records: dict[str, MailboxRecord] = {}
    for item in oauth_result.get("results", []):
        email = str(item.get("email_address") or item.get("email") or "").strip().lower()
        refresh_token = str(item.get("refresh_token") or "").strip()
        if not bool(item.get("success")) or not email or not refresh_token:
            continue
        token_records[email] = MailboxRecord(
            email=email,
            password=str(item.get("password") or "").strip(),
            refresh_token=refresh_token,
            access_token=str(item.get("access_token") or "").strip(),
            source=str(item.get("source") or "oauth").strip() or "oauth",
        )

    merged: list[MailboxRecord] = []
    for record in records:
        token_record = token_records.get(record.email)
        if token_record is None:
            merged.append(record)
            continue
        if not token_record.password:
            token_record = MailboxRecord(
                email=token_record.email,
                password=record.password,
                refresh_token=token_record.refresh_token,
                access_token=token_record.access_token,
                source=token_record.source,
            )
        merged.append(token_record)
    return merged


def run_auto_oauth_for_records(records: list[MailboxRecord]) -> tuple[list[MailboxRecord], dict]:
    summary = {
        "enabled": env_bool("OUTLOOK_REGISTER_ENABLE_OAUTH2", True),
        "processed": 0,
        "succeeded": 0,
        "failed": 0,
        "error_message": "",
    }
    if not summary["enabled"]:
        return records, summary

    targets = [record for record in records if record.email and record.password and not record.refresh_token]
    if not targets:
        return records, summary

    logger.info("starting automatic mailbox OAuth for %d newly registered mailbox(es)", len(targets))
    try:
        oauth_result = run_oauth(
            only_missing=True,
            limit=len(targets),
            accounts=targets,
        )
        if not isinstance(oauth_result, dict):
            raise TypeError("mailbox OAuth returned non-dict result")
        summary["processed"] = int(oauth_result.get("processed", 0))
        summary["succeeded"] = int(oauth_result.get("succeeded", 0))
        summary["failed"] = int(oauth_result.get("failed", 0))
        summary["error_message"] = str(oauth_result.get("error_message") or "")
        updated = records_with_oauth_results(records, oauth_result)
    except Exception as exc:
        logger.exception("automatic mailbox OAuth failed")
        summary["processed"] = len(targets)
        summary["failed"] = len(targets)
        summary["error_message"] = str(exc)
        return records, summary

    if summary["failed"]:
        logger.warning(
            "automatic mailbox OAuth failed for %s/%s mailbox(es): %s",
            summary["failed"],
            summary["processed"],
            summary["error_message"],
        )
    return updated, summary


def _mailbox_record_from_value(value) -> MailboxRecord:
    if isinstance(value, MailboxRecord):
        return value
    if isinstance(value, dict):
        return MailboxRecord(
            email=str(value.get("email_address") or value.get("email") or "").strip().lower(),
            password=str(value.get("password") or "").strip(),
            refresh_token=str(value.get("refresh_token") or "").strip(),
            access_token=str(value.get("access_token") or "").strip(),
            source=str(value.get("source") or "").strip(),
        )
    return MailboxRecord(
        email=str(getattr(value, "email_address", "") or getattr(value, "email", "")).strip().lower(),
        password=str(getattr(value, "password", "") or "").strip(),
        refresh_token=str(getattr(value, "refresh_token", "") or "").strip(),
        access_token=str(getattr(value, "access_token", "") or "").strip(),
        source=str(getattr(value, "source", "") or "").strip(),
    )


def append_token_record(record: MailboxRecord) -> None:
    if not record.refresh_token:
        return
    out_dir = results_dir()
    out_dir.mkdir(parents=True, exist_ok=True)
    token_file = out_dir / "outlook_token.txt"
    with token_file.open("a", encoding="utf-8") as handle:
        handle.write(
            f"{record.email}---{record.password}---{record.refresh_token}---{record.access_token}---0\n"
        )


def run_oauth(
    email_address: str = "",
    only_missing: bool = True,
    limit: int = 100,
    accounts: Iterable[MailboxRecord | dict] | None = None,
) -> dict:
    from camoufox_register import outlook_oauth

    requested_email = email_address.strip().lower()
    supplied = [_mailbox_record_from_value(account) for account in (accounts or [])]
    if not supplied:
        return {
            "success": False,
            "processed": 0,
            "succeeded": 0,
            "failed": 0,
            "error_message": "no mailbox accounts supplied for OAuth",
            "results": [],
        }

    max_items = min(max(limit or 100, 1), 500)
    targets: list[MailboxRecord] = []
    for record in supplied:
        if not record.email:
            continue
        if requested_email and record.email != requested_email:
            continue
        if only_missing and record.refresh_token:
            continue
        targets.append(record)
        if len(targets) >= max_items:
            break

    if not targets:
        return {
            "success": True,
            "processed": 0,
            "succeeded": 0,
            "failed": 0,
            "error_message": "",
            "results": [],
        }

    client_id = (
        env_str("OUTLOOK_REGISTER_OAUTH_CLIENT_ID")
        or env_str("OUTLOOK_OAUTH_CLIENT_ID")
        or DEFAULT_OAUTH_CLIENT_ID
    )
    redirect_url = (
        env_str("OUTLOOK_REGISTER_OAUTH_REDIRECT_URL")
        or env_str("OUTLOOK_OAUTH_REDIRECT_URL")
        or DEFAULT_OAUTH_REDIRECT_URL
    )
    scopes = split_scopes(env_str("OUTLOOK_REGISTER_OAUTH_SCOPES", DEFAULT_SCOPES))

    results = []
    succeeded = 0
    failed = 0
    for target in targets:
        if not target.password:
            failed += 1
            results.append({
                "email_address": target.email,
                "success": False,
                "error_message": "mailbox password is required for OAuth",
            })
            continue

        proxy = next_proxy()
        if proxy:
            logger.info("starting mailbox OAuth for %s with proxy %s", redact_email(target.email), redact_proxy(proxy))
        else:
            logger.info("starting mailbox OAuth for %s without proxy", redact_email(target.email))
        oauth = outlook_oauth(
            email=target.email,
            password=target.password,
            proxy=proxy,
            client_id=client_id,
            redirect_url=redirect_url,
            scopes=scopes,
        )
        if oauth.get("success"):
            succeeded += 1
            token_record = MailboxRecord(
                email=target.email,
                password=target.password,
                refresh_token=str(oauth.get("refresh_token") or ""),
                access_token=str(oauth.get("access_token") or ""),
                source="oauth",
            )
            append_token_record(token_record)
            results.append(record_response(token_record) | {
                "success": True,
                "error_message": "",
            })
        else:
            failed += 1
            results.append({
                "email_address": target.email,
                "success": False,
                "error_message": str(oauth.get("error") or "OAuth failed"),
            })

    return {
        "success": failed == 0 and succeeded > 0,
        "processed": len(targets),
        "succeeded": succeeded,
        "failed": failed,
        "error_message": "" if failed == 0 else f"mailbox OAuth failed: {failed}/{len(targets)}",
        "results": results,
    }


def run_once_locked() -> int:
    result = run_registration_request_locked(
        enabled=env_bool("OUTLOOK_REGISTER_ENABLED", False),
        import_only=env_bool("OUTLOOK_REGISTER_IMPORT_ONLY", False),
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if result.get("success"):
        return int(result.get("exit_code") or 0)
    return int(result.get("exit_code") or 1)


def run_once() -> int:
    try:
        with registration_lock():
            return run_once_locked()
    except RegistrationAlreadyRunning as exc:
        logger.warning("%s", exc)
        return env_int("OUTLOOK_REGISTER_LOCK_BUSY_EXIT_CODE", 1)


def run_registration_request_locked(enabled: bool, import_only: bool) -> dict:
    path = repo_dir()
    out_dir = results_dir()
    out_dir.mkdir(parents=True, exist_ok=True)

    before_records = {
        record.email: record
        for record in collect_records(out_dir, include_password_only=True)
    }

    if import_only:
        records = list(before_records.values())
        error_message = "" if records else "no mailbox records found to import"
        return {
            "success": bool(records),
            "exit_code": 0,
            "error_message": error_message,
            "accounts": [record_response(record) for record in records],
        }

    if not enabled:
        return {
            "success": False,
            "exit_code": 0,
            "error_message": "mailbox registration is disabled",
            "accounts": [],
        }

    proxy = next_proxy()
    write_register_config(path, proxy=proxy)
    clear_last_registration_error(out_dir)
    code = run_outlook_register(path, proxy=proxy)
    after_records = collect_records(out_dir, include_password_only=True)
    records = new_or_updated_records(before_records, after_records)
    if records:
        records, _ = run_auto_oauth_for_records(records)

    error_message = ""
    if not records:
        last_error = last_registration_error(out_dir)
        if last_error:
            error_message = f"mailbox registration failed: {last_error}"
        elif code != 0:
            error_message = f"mailbox registration failed with exit code {code}"
        else:
            error_message = "mailbox registration completed but returned no account records"

    return {
        "success": bool(records),
        "exit_code": code,
        "error_message": error_message,
        "accounts": [record_response(record) for record in records],
    }


def run_registration_request(enabled: bool, import_only: bool) -> dict:
    try:
        with registration_lock():
            return run_registration_request_locked(enabled=enabled, import_only=import_only)
    except RegistrationAlreadyRunning as exc:
        logger.warning("%s", exc)
        return {
            "success": False,
            "exit_code": env_int("OUTLOOK_REGISTER_LOCK_BUSY_EXIT_CODE", 1),
            "error_message": str(exc),
            "accounts": [],
        }


def configure_logging() -> None:
    level = env_str("LOG_LEVEL", "INFO").upper()
    handlers: list[logging.Handler] = [logging.StreamHandler()]
    log_file = env_str("OUTLOOK_REGISTER_LOG_FILE", str(results_dir() / "register.log"))
    if log_file:
        try:
            log_path = Path(log_file)
            log_path.parent.mkdir(parents=True, exist_ok=True)
            handlers.append(logging.FileHandler(log_path, encoding="utf-8"))
        except OSError as exc:
            print(f"could not open OutlookRegister log file {log_file}: {exc}", file=sys.stderr)
    logging.basicConfig(
        level=getattr(logging, level, logging.INFO),
        format="%(asctime)s %(levelname)s %(message)s",
        handlers=handlers,
        force=True,
    )


def main(argv: list[str] | None = None) -> int:
    configure_logging()
    parser = argparse.ArgumentParser(description="Run OutlookRegister and return registered mailbox accounts.")
    parser.add_argument("command", choices=("run", "import", "config", "oauth", "oauth-worker"), nargs="?", default="run")
    parser.add_argument("--email", default="", help="single mailbox email for oauth")
    parser.add_argument("--all", action="store_true", help="include mailboxes that already have refresh tokens")
    parser.add_argument("--limit", type=int, default=100, help="maximum mailboxes for oauth")
    args = parser.parse_args(argv)

    if args.command == "config":
        print(json.dumps(build_register_config(), ensure_ascii=False, indent=2))
        return 0

    if args.command == "oauth-worker":
        return 1

    if args.command == "oauth":
        result = run_oauth(email_address=args.email, only_missing=not args.all, limit=args.limit)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0 if result.get("success") else 1

    if args.command == "import":
        os.environ["OUTLOOK_REGISTER_IMPORT_ONLY"] = "true"
        return run_once()

    interval = env_int("OUTLOOK_REGISTER_INTERVAL_SECONDS", 0)
    if interval > 0 and not env_bool("OUTLOOK_REGISTER_ENABLED", False):
        logger.info("OUTLOOK_REGISTER_ENABLED=false; ignoring OUTLOOK_REGISTER_INTERVAL_SECONDS=%s", interval)
        interval = 0
    while True:
        code = run_once()
        if interval <= 0:
            return code
        logger.info("sleeping %ss before next OutlookRegister run", interval)
        time.sleep(interval)


if __name__ == "__main__":
    raise SystemExit(main())
