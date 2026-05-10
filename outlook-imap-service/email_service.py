from __future__ import annotations

import html
import json
import logging
import os
import re
import secrets
import signal
import threading
import time
from concurrent import futures
from dataclasses import dataclass
from datetime import datetime
from typing import Iterable
from urllib.parse import urlparse

import grpc
import psycopg
import requests
from psycopg.rows import dict_row

import email_pb2
import email_pb2_grpc


DEFAULT_LISTEN_ADDR = ":50051"
DEFAULT_OAUTH_CLIENT_ID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
DEFAULT_OAUTH_SCOPE = "https://graph.microsoft.com/Mail.Read"
DEFAULT_TOKEN_URL = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
DEFAULT_GRAPH_MESSAGES_URL = "https://graph.microsoft.com/v1.0/me/messages"
DEFAULT_POLL_INTERVAL_SECONDS = 5
DEFAULT_MESSAGE_LIMIT = 25
DEFAULT_HTTP_TIMEOUT_SECONDS = 20
DEFAULT_ALIAS_TOKEN_LENGTH = 6

STATUS_AVAILABLE = "AVAILABLE"
STATUS_ASSIGNED = "ASSIGNED"
STATUS_REGISTERED = "REGISTERED"
STATUS_OAUTH_PENDING = "OAUTH_PENDING"
STATUS_USER_ALREADY_EXISTS = "USER_ALREADY_EXISTS"
STATUS_AUTH_FAILED = "AUTH_FAILED"
STATUS_BLOCKED = "BLOCKED"

OTP_PATTERN = re.compile(r"(^|[^0-9])([0-9]{6})([^0-9]|$)")
EMAIL_PATTERN = re.compile(r"(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}")
HTML_TAG_PATTERN = re.compile(r"<[^>]+>")

logger = logging.getLogger("outlook-email-service")


def env_str(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def env_int(name: str, default: int) -> int:
    value = env_str(name)
    if not value:
        return default
    try:
        return int(value)
    except ValueError:
        return default


def normalize_scope(value: str) -> str:
    return " ".join(value.replace(",", " ").split())


def normalize_email(email: str) -> str:
    return email.strip().lower()


def canonical_email(email: str) -> str:
    normalized = normalize_email(email)
    local, sep, domain = normalized.partition("@")
    if not sep or not local or not domain:
        return normalized
    local = local.split("+", 1)[0]
    return f"{local}@{domain}"


def redact_email(email: str) -> str:
    local, sep, domain = email.strip().partition("@")
    if not sep:
        return "***"
    return f"{local[:2] + '***' if len(local) > 2 else '***'}@{domain}"


def contains_fold(value: str, keyword: str) -> bool:
    if not keyword:
        return True
    return keyword.lower() in value.lower()


def extract_otp(body: str) -> str:
    body = html.unescape(body or "").replace("\u00a0", " ")
    body = HTML_TAG_PATTERN.sub(" ", body)
    match = OTP_PATTERN.search(body)
    return match.group(2) if match else ""


def parse_graph_time(value: str) -> float:
    if not value:
        return 0
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except Exception:
        return 0


def pb_mailbox(row: dict | None) -> email_pb2.EmailMailbox | None:
    if row is None:
        return None
    return email_pb2.EmailMailbox(
        email_address=row["email"],
        password=row["password"],
        refresh_token=row["refresh_token"],
        access_token=row["access_token"],
        status=row["status"],
        last_error=row["last_error"],
        is_primary=bool(row["is_primary"]),
        primary_email=row["primary_email"],
        created_at=int(row["created_at"]),
        updated_at=int(row["updated_at"]),
    )


class MailboxStore:
    def __init__(self, dsn: str, alias_token_length: int):
        if not dsn:
            raise RuntimeError("PG_DSN is required")
        self.dsn = dsn
        self.alias_token_length = alias_token_length or DEFAULT_ALIAS_TOKEN_LENGTH
        self.ensure_schema()

    def connect(self):
        return psycopg.connect(self.dsn, row_factory=dict_row)

    def ensure_schema(self) -> None:
        statements = [
            """CREATE TABLE IF NOT EXISTS mailboxes (
                id TEXT PRIMARY KEY,
                email TEXT NOT NULL UNIQUE,
                password TEXT NOT NULL DEFAULT '',
                refresh_token TEXT NOT NULL DEFAULT '',
                access_token TEXT NOT NULL DEFAULT '',
                status TEXT NOT NULL DEFAULT 'AVAILABLE',
                last_error TEXT NOT NULL DEFAULT '',
                is_primary BOOLEAN NOT NULL DEFAULT false,
                primary_email TEXT NOT NULL DEFAULT '',
                created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
                updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
            )""",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS password TEXT NOT NULL DEFAULT ''",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS refresh_token TEXT NOT NULL DEFAULT ''",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS access_token TEXT NOT NULL DEFAULT ''",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'AVAILABLE'",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT ''",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS primary_email TEXT NOT NULL DEFAULT ''",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT",
            "ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT",
            "DROP INDEX IF EXISTS idx_mailboxes_assigned_account",
            "ALTER TABLE mailboxes DROP COLUMN IF EXISTS assigned_account_id",
            "CREATE INDEX IF NOT EXISTS idx_mailboxes_status ON mailboxes(status)",
            "CREATE INDEX IF NOT EXISTS idx_mailboxes_primary ON mailboxes(primary_email)",
            (
                "UPDATE mailboxes SET status = 'OAUTH_PENDING', last_error = '' "
                "WHERE status = 'AUTH_FAILED' "
                "AND last_error = 'registered mailbox has no OAuth refresh token'"
            ),
        ]
        with self.connect() as conn:
            with conn.cursor() as cur:
                for statement in statements:
                    cur.execute(statement)

    def upsert_mailbox(self, mailbox: email_pb2.EmailMailbox) -> email_pb2.EmailMailbox:
        email = normalize_email(mailbox.email_address)
        if not email:
            raise ValueError("email_address is required")
        is_primary = bool(mailbox.is_primary)
        primary_email = normalize_email(mailbox.primary_email)
        if not primary_email:
            primary_email = email if is_primary else canonical_email(email)
        if primary_email == email:
            is_primary = True
        requested_status = mailbox.status.strip()
        status = requested_status or STATUS_AVAILABLE
        now = int(time.time())
        row_id = secrets.token_hex(16)

        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO mailboxes (
                        id, email, password, refresh_token, access_token, status,
                        last_error, is_primary, primary_email, created_at, updated_at
                    ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
                    ON CONFLICT (email) DO UPDATE SET
                        password = CASE WHEN EXCLUDED.password <> '' THEN EXCLUDED.password ELSE mailboxes.password END,
                        refresh_token = CASE WHEN EXCLUDED.refresh_token <> '' THEN EXCLUDED.refresh_token ELSE mailboxes.refresh_token END,
                        access_token = CASE WHEN EXCLUDED.access_token <> '' THEN EXCLUDED.access_token ELSE mailboxes.access_token END,
                        status = CASE WHEN %s <> '' THEN EXCLUDED.status ELSE mailboxes.status END,
                        last_error = CASE WHEN %s <> '' OR EXCLUDED.last_error <> '' THEN EXCLUDED.last_error ELSE mailboxes.last_error END,
                        is_primary = EXCLUDED.is_primary,
                        primary_email = EXCLUDED.primary_email,
                        updated_at = EXCLUDED.updated_at
                    """,
                    (
                        row_id,
                        email,
                        mailbox.password,
                        mailbox.refresh_token.strip(),
                        mailbox.access_token.strip(),
                        status,
                        mailbox.last_error.strip(),
                        is_primary,
                        primary_email,
                        now,
                        now,
                        requested_status,
                        requested_status,
                    ),
                )
        return self.find_mailbox(email)

    def list_mailboxes(self, status: str, limit: int) -> list[email_pb2.EmailMailbox]:
        limit = min(max(limit or 100, 1), 500)
        args: list = []
        query = SELECT_MAILBOX + " WHERE 1=1"
        if status.strip():
            args.append(status.strip())
            query += " AND status = %s"
        args.append(limit)
        query += " ORDER BY updated_at DESC LIMIT %s"
        with self.connect() as conn, conn.cursor() as cur:
            cur.execute(query, args)
            return [pb_mailbox(row) for row in cur.fetchall()]

    def acquire_email(self, excludes: Iterable[str]) -> email_pb2.EmailMailbox:
        exclude_set = [normalize_email(e) for e in excludes if normalize_email(e)]
        with self.connect() as conn:
            with conn.transaction():
                with conn.cursor() as cur:
                    row = self._acquire_existing(
                        cur,
                        exclude_set,
                        "is_primary = true AND refresh_token <> ''",
                    )
                    if row:
                        return pb_mailbox(row)
                    row = self._acquire_existing(
                        cur,
                        exclude_set,
                        "is_primary = false AND refresh_token <> '' AND primary_email IN (SELECT email FROM mailboxes WHERE is_primary = true AND status = %s AND refresh_token <> '')",
                        [STATUS_REGISTERED],
                    )
                    if row:
                        return pb_mailbox(row)
                    row = self._create_assigned_alias(cur, exclude_set)
                    if row:
                        return pb_mailbox(row)
        raise RuntimeError("no available mailbox")

    def _acquire_existing(self, cur, excludes: list[str], condition: str, condition_args: list | None = None):
        args: list = [STATUS_AVAILABLE]
        if condition_args:
            args.extend(condition_args)
        query = SELECT_MAILBOX + " WHERE status = %s AND " + condition
        if excludes:
            query += " AND email NOT IN (" + ",".join(["%s"] * len(excludes)) + ")"
            args.extend(excludes)
        query += " ORDER BY updated_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED"
        cur.execute(query, args)
        row = cur.fetchone()
        if not row:
            return None
        self._assign_mailbox(cur, row["email"])
        row["status"] = STATUS_ASSIGNED
        row["last_error"] = ""
        return row

    def _create_assigned_alias(self, cur, excludes: list[str]):
        cur.execute(
            SELECT_MAILBOX
            + " WHERE is_primary = true AND status = %s AND refresh_token <> '' ORDER BY updated_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED",
            (STATUS_REGISTERED,),
        )
        primary = cur.fetchone()
        if not primary:
            return None
        for _ in range(20):
            alias = self._make_alias(primary["email"])
            if alias in excludes:
                continue
            now = int(time.time())
            row = {
                "id": secrets.token_hex(16),
                "email": alias,
                "password": primary["password"],
                "refresh_token": primary["refresh_token"],
                "access_token": primary["access_token"],
                "status": STATUS_ASSIGNED,
                "last_error": "",
                "is_primary": False,
                "primary_email": primary["email"],
                "created_at": now,
                "updated_at": now,
            }
            cur.execute(
                """
                INSERT INTO mailboxes (
                    id, email, password, refresh_token, access_token, status,
                    last_error, is_primary, primary_email, created_at, updated_at
                ) VALUES (%s,%s,%s,%s,%s,%s,'',%s,%s,%s,%s)
                ON CONFLICT (email) DO NOTHING
                """,
                (
                    row["id"],
                    row["email"],
                    row["password"],
                    row["refresh_token"],
                    row["access_token"],
                    row["status"],
                    row["is_primary"],
                    row["primary_email"],
                    now,
                    now,
                ),
            )
            if cur.rowcount > 0:
                return row
        raise RuntimeError(f"failed to create unique alias for {redact_email(primary['email'])}")

    def _make_alias(self, primary: str) -> str:
        local, sep, domain = normalize_email(primary).partition("@")
        if not sep or not local or not domain:
            raise ValueError(f"invalid primary email: {redact_email(primary)}")
        alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
        token = "".join(secrets.choice(alphabet) for _ in range(self.alias_token_length))
        return f"{local}+{token}@{domain}"

    def _assign_mailbox(self, cur, email: str) -> None:
        cur.execute(
            "UPDATE mailboxes SET status = %s, last_error = '', updated_at = %s WHERE email = %s",
            (STATUS_ASSIGNED, int(time.time()), email),
        )

    def mark_email_status(self, email: str, status: str, last_error: str) -> email_pb2.EmailMailbox:
        email = normalize_email(email)
        status = status.strip()
        if not email:
            raise ValueError("email_address is required")
        if not status:
            raise ValueError("status is required")
        with self.connect() as conn:
            with conn.transaction():
                with conn.cursor() as cur:
                    cur.execute(SELECT_MAILBOX + " WHERE email = %s FOR UPDATE", (email,))
                    row = cur.fetchone()
                    if not row:
                        raise RuntimeError(f"mailbox not found: {redact_email(email)}")
                    cur.execute(
                        "UPDATE mailboxes SET status = %s, last_error = %s, updated_at = %s WHERE email = %s",
                        (status, last_error.strip(), int(time.time()), email),
                    )
                    if status == STATUS_USER_ALREADY_EXISTS:
                        primary = row["primary_email"] or row["email"]
                        cur.execute(
                            "UPDATE mailboxes SET status = %s, last_error = %s, updated_at = %s WHERE email = %s AND is_primary = true AND status <> %s",
                            (STATUS_BLOCKED, last_error.strip(), int(time.time()), primary, STATUS_USER_ALREADY_EXISTS),
                        )
                        cur.execute(
                            "UPDATE mailboxes SET status = %s, last_error = %s, updated_at = %s WHERE primary_email = %s AND is_primary = false AND status = %s",
                            (STATUS_BLOCKED, last_error.strip(), int(time.time()), primary, STATUS_AVAILABLE),
                        )
        return self.find_mailbox(email)

    def find_mailbox(self, email: str) -> email_pb2.EmailMailbox:
        with self.connect() as conn, conn.cursor() as cur:
            cur.execute(SELECT_MAILBOX + " WHERE email = %s", (normalize_email(email),))
            row = cur.fetchone()
            if not row:
                raise RuntimeError(f"mailbox not found: {redact_email(email)}")
            return pb_mailbox(row)

    def poll_mailbox_for_email(self, email: str) -> email_pb2.EmailMailbox:
        email = normalize_email(email)
        with self.connect() as conn, conn.cursor() as cur:
            cur.execute(SELECT_MAILBOX + " WHERE email = %s", (email,))
            row = cur.fetchone()
            if not row:
                canonical = canonical_email(email)
                if canonical and canonical != email:
                    cur.execute(SELECT_MAILBOX + " WHERE email = %s AND is_primary = true", (canonical,))
                    row = cur.fetchone()
            if not row:
                raise RuntimeError(f"mailbox not found: {redact_email(email)}")
            primary_email = row["email"] if row["is_primary"] else (row["primary_email"] or canonical_email(row["email"]))
            cur.execute(SELECT_MAILBOX + " WHERE email = %s AND is_primary = true", (primary_email,))
            primary = cur.fetchone()
            if not primary:
                raise RuntimeError(f"primary mailbox not found for {redact_email(email)}")
            if not primary["refresh_token"].strip():
                raise RuntimeError(f"primary mailbox has no refresh token: {redact_email(primary['email'])}")
            if primary["status"] in {STATUS_AUTH_FAILED, STATUS_BLOCKED}:
                raise RuntimeError(f"primary mailbox is not pollable: {redact_email(primary['email'])} status={primary['status']}")
            return pb_mailbox(primary)

    def update_mailbox_tokens(self, email: str, refresh_token: str, access_token: str) -> None:
        with self.connect() as conn, conn.cursor() as cur:
            cur.execute(
                "UPDATE mailboxes SET refresh_token = %s, access_token = %s, updated_at = %s WHERE email = %s",
                (refresh_token.strip(), access_token.strip(), int(time.time()), normalize_email(email)),
            )

    def mark_auth_failed(self, email: str, err: Exception) -> None:
        try:
            self.mark_email_status(email, STATUS_AUTH_FAILED, str(err))
        except Exception as update_err:
            logger.warning("[MAIL] failed to mark mailbox auth failed for %s: %s", redact_email(email), update_err)


SELECT_MAILBOX = """
    SELECT id, email, password, refresh_token, access_token, status,
        last_error, is_primary, primary_email, created_at, updated_at
    FROM mailboxes
"""


class OAuthManager:
    def __init__(self, refresh_token: str, *, allow_device_flow: bool = False):
        self.refresh_token = refresh_token.strip()
        self.access_token = ""
        self.expires_at = 0.0
        self.client_id = env_str("OUTLOOK_OAUTH_CLIENT_ID", DEFAULT_OAUTH_CLIENT_ID) or DEFAULT_OAUTH_CLIENT_ID
        self.scope = normalize_scope(env_str("OUTLOOK_OAUTH_SCOPE", DEFAULT_OAUTH_SCOPE)) or DEFAULT_OAUTH_SCOPE
        self.token_url = env_str("OUTLOOK_OAUTH_TOKEN_URL", DEFAULT_TOKEN_URL) or DEFAULT_TOKEN_URL
        self.timeout = env_int("OUTLOOK_HTTP_TIMEOUT_SECONDS", DEFAULT_HTTP_TIMEOUT_SECONDS)
        self.allow_device_flow = allow_device_flow
        self.lock = threading.Lock()

    def get_access_token(self) -> str:
        with self.lock:
            if self.access_token and time.time() < self.expires_at - 60:
                return self.access_token
            return self._refresh_locked()

    def refresh_access_token(self) -> str:
        with self.lock:
            return self._refresh_locked()

    def current_tokens(self) -> tuple[str, str]:
        with self.lock:
            return self.refresh_token, self.access_token

    def _refresh_locked(self) -> str:
        if not self.refresh_token:
            raise RuntimeError("refresh token is missing")
        resp = requests.post(
            self.token_url,
            data={
                "grant_type": "refresh_token",
                "client_id": self.client_id,
                "refresh_token": self.refresh_token,
                "scope": self.scope,
            },
            headers={"Content-Type": "application/x-www-form-urlencoded"},
            timeout=self.timeout,
        )
        try:
            body = resp.json()
        except json.JSONDecodeError:
            body = {"raw": resp.text}
        if resp.status_code != 200:
            raise RuntimeError(f"token refresh failed: {body}")
        access_token = body.get("access_token", "")
        if not access_token:
            raise RuntimeError("token refresh returned empty access token")
        self.access_token = access_token
        if body.get("refresh_token"):
            self.refresh_token = body["refresh_token"]
        self.expires_at = time.time() + int(body.get("expires_in") or 3600)
        return self.access_token


@dataclass
class CachedOTP:
    otp: str
    subject: str
    source_email: str
    received_at: float


class GraphFetchError(RuntimeError):
    def __init__(self, status_code: int, body: str, retry_after: float = 0):
        super().__init__(f"status={status_code} body={body[:500]}")
        self.status_code = status_code
        self.retry_after = retry_after

    @property
    def is_auth(self) -> bool:
        return self.status_code in {401, 403}

    @property
    def retryable(self) -> bool:
        return self.status_code == 429 or self.status_code >= 500


class MailWatcher:
    def __init__(self, store: MailboxStore):
        self.store = store
        self.graph_url = env_str("OUTLOOK_GRAPH_MESSAGES_URL", DEFAULT_GRAPH_MESSAGES_URL) or DEFAULT_GRAPH_MESSAGES_URL
        self.message_limit = min(max(env_int("OUTLOOK_MESSAGE_LIMIT", DEFAULT_MESSAGE_LIMIT), 1), 100)
        self.poll_interval = max(env_int("OUTLOOK_POLL_INTERVAL_SECONDS", DEFAULT_POLL_INTERVAL_SECONDS), 1)
        self.timeout = env_int("OUTLOOK_HTTP_TIMEOUT_SECONDS", DEFAULT_HTTP_TIMEOUT_SECONDS)
        self.started_at = time.time() - 30
        self.lock = threading.Lock()
        self.cached_otps: dict[str, CachedOTP] = {}
        self.seen_messages: dict[str, float] = {}
        self.oauth_managers: dict[str, tuple[str, OAuthManager]] = {}

    def consume_cached_otp(self, email: str, subject_keyword: str, issued_after: float) -> str | None:
        with self.lock:
            self._cleanup_locked()
            key = normalize_email(email)
            cached = self.cached_otps.get(key)
            if cached is None:
                canonical = canonical_email(email)
                key = canonical
                cached = self.cached_otps.get(key)
            if cached is None:
                return None
            if not contains_fold(cached.subject, subject_keyword):
                return None
            if issued_after and cached.received_at < issued_after:
                return None
            del self.cached_otps[key]
            logger.info("[MAIL] Served cached OTP for %s", redact_email(email))
            return cached.otp

    def poll_for_email(self, email: str) -> None:
        mailbox = self.store.poll_mailbox_for_email(email)
        self._poll_mailbox(mailbox)

    def _poll_mailbox(self, mailbox: email_pb2.EmailMailbox) -> None:
        manager = self._oauth_manager_for_mailbox(mailbox)
        try:
            access_token = manager.get_access_token()
            self._persist_tokens(mailbox, manager)
            messages = self._fetch_recent_messages(access_token)
        except GraphFetchError as err:
            if not err.is_auth:
                raise
            logger.info("[MAIL] Graph auth error for %s; refreshing token and retrying", redact_email(mailbox.email_address))
            try:
                access_token = manager.refresh_access_token()
                self._persist_tokens(mailbox, manager)
                messages = self._fetch_recent_messages(access_token)
            except Exception as refresh_err:
                self.store.mark_auth_failed(mailbox.email_address, refresh_err)
                raise
        except Exception as err:
            self.store.mark_auth_failed(mailbox.email_address, err)
            raise
        self._process_messages(mailbox.email_address, messages)

    def _oauth_manager_for_mailbox(self, mailbox: email_pb2.EmailMailbox) -> OAuthManager:
        key = normalize_email(mailbox.email_address)
        refresh_token = mailbox.refresh_token.strip()
        with self.lock:
            entry = self.oauth_managers.get(key)
            if entry is None or entry[0] != refresh_token:
                entry = (refresh_token, OAuthManager(refresh_token))
                self.oauth_managers[key] = entry
            return entry[1]

    def _persist_tokens(self, mailbox: email_pb2.EmailMailbox, manager: OAuthManager) -> None:
        refresh_token, access_token = manager.current_tokens()
        if refresh_token != mailbox.refresh_token or access_token != mailbox.access_token:
            self.store.update_mailbox_tokens(mailbox.email_address, refresh_token, access_token)

    def _fetch_recent_messages(self, access_token: str) -> list[dict]:
        last_err: Exception | None = None
        for attempt in range(3):
            try:
                return self._fetch_once(access_token)
            except GraphFetchError as err:
                last_err = err
                if attempt == 2 or not err.retryable:
                    break
                time.sleep(err.retry_after or (attempt + 1) * 0.5)
        raise last_err or RuntimeError("Graph fetch failed")

    def _fetch_once(self, access_token: str) -> list[dict]:
        resp = requests.get(
            self.graph_url,
            params={
                "$top": str(self.message_limit),
                "$orderby": "receivedDateTime desc",
                "$select": "id,subject,bodyPreview,body,toRecipients,ccRecipients,bccRecipients,internetMessageHeaders,receivedDateTime",
            },
            headers={
                "Authorization": "Bearer " + access_token,
                "Accept": "application/json",
                "Prefer": 'outlook.body-content-type="text"',
            },
            timeout=self.timeout,
        )
        if resp.status_code < 200 or resp.status_code >= 300:
            retry_after = 0.0
            if resp.headers.get("Retry-After", "").isdigit():
                retry_after = min(float(resp.headers["Retry-After"]), 10.0)
            raise GraphFetchError(resp.status_code, resp.text, retry_after)
        return resp.json().get("value", [])

    def _process_messages(self, source_email: str, messages: list[dict]) -> None:
        for msg in messages:
            msg_key = source_email + ":" + self._message_key(msg)
            with self.lock:
                if msg_key in self.seen_messages:
                    continue
                self.seen_messages[msg_key] = time.time()
            received_at = parse_graph_time(msg.get("receivedDateTime", ""))
            recipients = self._message_addresses(msg)
            if not recipients:
                continue
            body = (msg.get("bodyPreview") or "") + "\n" + (msg.get("body") or {}).get("content", "")
            otp = extract_otp(body)
            if not otp:
                continue
            self._cache_otp(msg.get("subject", ""), otp, recipients, received_at or time.time())

    def _cache_otp(self, subject: str, otp: str, recipients: list[str], received_at: float) -> None:
        with self.lock:
            for recipient in recipients:
                key = normalize_email(recipient)
                if key:
                    self.cached_otps[key] = CachedOTP(otp, subject, recipient, received_at)
            logger.info("[MAIL] Cached OTP for %s recipient(s)", len(recipients))

    def _cleanup_locked(self) -> None:
        now = time.time()
        for key, cached in list(self.cached_otps.items()):
            if now - cached.received_at > 600:
                del self.cached_otps[key]
        for key, seen_at in list(self.seen_messages.items()):
            if now - seen_at > 3600:
                del self.seen_messages[key]

    def _message_key(self, msg: dict) -> str:
        return msg.get("id") or str(hash(json.dumps(msg, sort_keys=True)))

    def _message_addresses(self, msg: dict) -> list[str]:
        out: list[str] = []
        for field in ("toRecipients", "ccRecipients", "bccRecipients"):
            for recipient in msg.get(field) or []:
                address = ((recipient.get("emailAddress") or {}).get("address") or "").strip()
                if address:
                    out.append(address)
        for header in msg.get("internetMessageHeaders") or []:
            name = (header.get("name") or "").strip().lower()
            value = header.get("value") or ""
            if name in RECIPIENT_HEADERS:
                out.extend(EMAIL_PATTERN.findall(value))
            elif name == "received":
                idx = value.lower().rfind(" for ")
                if idx >= 0:
                    out.extend(EMAIL_PATTERN.findall(value[idx + 5 :]))
        return out


RECIPIENT_HEADERS = {
    "to",
    "cc",
    "bcc",
    "delivered-to",
    "envelope-to",
    "x-envelope-to",
    "x-original-to",
    "x-original-recipient",
    "resent-to",
    "apparently-to",
    "x-forwarded-to",
    "x-ms-exchange-organization-originalrecipient",
    "x-ms-exchange-organization-originalenveloperecipients",
}


class EmailService(email_pb2_grpc.EmailServiceServicer):
    def __init__(self, store: MailboxStore, watcher: MailWatcher):
        self.store = store
        self.watcher = watcher

    def GetEmail(self, request, context):
        try:
            mailbox = self.store.acquire_email(request.exclude_email_addresses)
            return email_pb2.GetEmailResponse(
                email_address=mailbox.email_address,
                password=mailbox.password,
                mailbox=mailbox,
            )
        except Exception as err:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(err))

    def MarkEmailStatus(self, request, context):
        try:
            mailbox = self.store.mark_email_status(
                request.email_address,
                request.status,
                request.last_error,
            )
            return email_pb2.MarkEmailStatusResponse(mailbox=mailbox)
        except Exception as err:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(err))

    def UpsertMailbox(self, request, context):
        try:
            mailbox = self.store.upsert_mailbox(request.mailbox)
            return email_pb2.UpsertEmailMailboxResponse(mailbox=mailbox)
        except Exception as err:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(err))

    def ListMailboxes(self, request, context):
        try:
            return email_pb2.ListEmailMailboxesResponse(
                mailboxes=self.store.list_mailboxes(request.status, request.limit)
            )
        except Exception as err:
            context.abort(grpc.StatusCode.INTERNAL, str(err))

    def WaitForEmail(self, request, context):
        timeout = request.timeout_seconds or 300
        issued_after = float(request.issued_after_unix or 0)
        deadline = time.time() + timeout
        try:
            cached = self.watcher.consume_cached_otp(request.email_address, request.subject_keyword, issued_after)
            if cached:
                return email_pb2.WaitForEmailResponse(found=True, content_extracted=cached)
            self.watcher.poll_for_email(request.email_address)
            cached = self.watcher.consume_cached_otp(request.email_address, request.subject_keyword, issued_after)
            if cached:
                return email_pb2.WaitForEmailResponse(found=True, content_extracted=cached)
            while time.time() < deadline:
                if context.is_active() is False:
                    context.abort(grpc.StatusCode.CANCELLED, "request cancelled")
                time.sleep(min(self.watcher.poll_interval, max(deadline - time.time(), 0)))
                self.watcher.poll_for_email(request.email_address)
                cached = self.watcher.consume_cached_otp(request.email_address, request.subject_keyword, issued_after)
                if cached:
                    return email_pb2.WaitForEmailResponse(found=True, content_extracted=cached)
            return email_pb2.WaitForEmailResponse(found=False)
        except Exception as err:
            context.abort(grpc.StatusCode.INTERNAL, str(err))

def grpc_listen_addr(value: str) -> str:
    value = value or DEFAULT_LISTEN_ADDR
    if value.startswith(":"):
        return "[::]" + value
    parsed = urlparse("//" + value)
    if parsed.hostname and parsed.port:
        return value
    return value


def serve() -> None:
    logging.basicConfig(level=getattr(logging, env_str("LOG_LEVEL", "INFO").upper(), logging.INFO), format="%(asctime)s %(levelname)s %(message)s")
    store = MailboxStore(env_str("PG_DSN"), env_int("OUTLOOK_ALIAS_RANDOM_LENGTH", DEFAULT_ALIAS_TOKEN_LENGTH))
    watcher = MailWatcher(store)
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=32))
    email_pb2_grpc.add_EmailServiceServicer_to_server(EmailService(store, watcher), server)
    listen_addr = grpc_listen_addr(env_str("LISTEN_ADDR", DEFAULT_LISTEN_ADDR))
    server.add_insecure_port(listen_addr)
    server.start()
    logger.info("Starting Python Outlook mail gRPC server on %s", listen_addr)

    stop_event = threading.Event()

    def stop(signum, frame):
        logger.info("received signal %s; stopping", signum)
        server.stop(5)
        stop_event.set()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    stop_event.wait()


if __name__ == "__main__":
    serve()
