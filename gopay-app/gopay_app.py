"""
GoPay 一号一Plus 步骤脚本

流程：改号 → 注销 → 登录 → 充值 → 支付 → 解绑

用法：
  # 单步执行
  python3 gopay_app.py --step change-phone --new-phone <new-phone>
  python3 gopay_app.py --step deactivate
  python3 gopay_app.py --step login --phone <phone>
  python3 gopay_app.py --step unlink
  python3 gopay_app.py --step status

  # 完整步骤（接入支付流程）
  python3 gopay_app.py --step account-setup --phone <phone> --pin <pin>

环境变量：
  GOPAY_PROXY_POOL - GoPay 代理池，429 时按转轮轮换
  ORCHESTRATOR_URL - orchestrator gRPC 地址 (默认 127.0.0.1:50051)
"""

import argparse
import base64
import json
import os
import re
import sys
import threading
import time

from gopay_client import (
    GopayClient,
    GOTO_CLIENT_ID,
    GOTO_CLIENT_SECRET,
    ensure_device_fingerprint,
    generate_random_device_fingerprint,
)
from replay import LinkedAppUnlinkOptions, run_linked_app_unlink

GOPAY_SIGNUP_AUTH_UUID = "bb648413-b637-443a-8ebf-176cf9b5dc32"

GOPAY_API = "https://customer.gopayapi.com"
GOPAY_CUSTOMER = GOPAY_API
GOJEK_API = "https://api.gojekapi.com"
GOTO_AUTH = "https://accounts.goto-products.com"

ORCHESTRATOR_URL = os.environ.get("ORCHESTRATOR_URL", "http://127.0.0.1:8080")
GOPAY_PIN = os.environ.get("GOPAY_PIN", "")
GOPAY_COUNTRY_CODE = os.environ.get("GOPAY_COUNTRY_CODE", "62")
GOPAY_LOGIN_OTP_METHOD = os.environ.get("GOPAY_LOGIN_OTP_METHOD", "otp_wa")
GOPAY_PIN_CLIENT_ID = os.environ.get("GOPAY_PIN_CLIENT_ID", "6d11d261d7ae462dbd4be0dc5f36a697-MFAGOJEK")
GOPAY_CHANGE_PHONE_COUNTRY_SYNC = os.environ.get("GOPAY_CHANGE_PHONE_COUNTRY_SYNC", "").strip().lower() in {"1", "true", "yes", "on"}
GOPAY_TOKEN_REFRESH_MIN_TTL_SECONDS = int(os.environ.get("GOPAY_TOKEN_REFRESH_MIN_TTL_SECONDS", "900"))
GOPAY_OTP_TIMEOUT_SECONDS = int(os.environ.get("GOPAY_OTP_TIMEOUT_SECONDS", "180"))
GOPAY_MIN_BALANCE_RP = 1
_STATE_LOCK = threading.RLock()
_GOPAY_PROXY_STATE_KEY = "_gopay_proxy"
LOGIN_STATE_KEYS = (
    "_login_phone",
    "_login_country_code",
    "_login_verification_id",
    "_login_verification_method",
    "_login_otp_token",
    "_login_2fa_token",
    "_login_started_at",
    "_login_otp_sent_at",
    "_login_otp_expires_at",
)
SIGNUP_ACCOUNT_STATE_KEYS = (
    "_signup_phone",
    "_signup_country_code",
    "_signup_name",
    "_signup_email",
)
SIGNUP_OTP_STATE_KEYS = (
    "_signup_verification_id",
    "_signup_verification_method",
    "_signup_otp_token",
    "_signup_started_at",
    "_signup_otp_sent_at",
    "_signup_otp_expires_at",
)
SIGNUP_PIN_STATE_KEYS = (
    "_signup_pin_verification_id",
    "_signup_pin_verification_method",
    "_signup_pin_otp_token",
    "_signup_pin_challenge_id",
    "_signup_pin_client_id",
    "_signup_pin_otp_sent_at",
    "_signup_pin_otp_expires_at",
)
SIGNUP_STATE_KEYS = SIGNUP_ACCOUNT_STATE_KEYS + SIGNUP_OTP_STATE_KEYS + SIGNUP_PIN_STATE_KEYS
ACTIVE_TOKEN_STATE_KEYS = (
    "token",
    "refresh_token",
    "token_expires_at",
)
ACTIVE_TOKEN_METADATA_KEYS = (
    "last_token_refresh_at",
    "last_token_refresh_error",
    "last_token_refresh_failed_at",
)
TMP_TOKEN_STATE_KEYS = tuple(f"_tmp_{key}" for key in ACTIVE_TOKEN_STATE_KEYS)
TMP_TOKEN_METADATA_KEYS = (
    "_tmp_phone",
    "_tmp_token_migrated_at",
)


class GopayProxyPoolExhausted(RuntimeError):
    pass


def safe_response_json(response: dict) -> str:
    try:
        return json.dumps(response or {}, ensure_ascii=False, separators=(",", ":"), default=str)
    except Exception as exc:
        return json.dumps({"marshal_error": str(exc)}, ensure_ascii=False, separators=(",", ":"))


def log_api_response(label: str, response: dict) -> None:
    print(f"[gopay-app] {label}: {safe_response_json(response)}", flush=True)


def gopay_proxy_pool_entries() -> list[str]:
    raw = os.environ.get("GOPAY_PROXY_POOL", "").strip()
    if not raw:
        return []
    return [item.strip() for item in re.split(r"[\s,]+", raw) if item.strip()]


def _require_gopay_proxy_pool() -> list[str]:
    entries = gopay_proxy_pool_entries()
    if not entries:
        raise RuntimeError("GOPAY_PROXY_POOL is required")
    return entries


def _proxy_index(entries: list[str], proxy: str) -> int:
    try:
        return entries.index(str(proxy or "").strip())
    except ValueError:
        return -1


def _state_proxy_index(state: dict, entries: list[str]) -> int:
    if isinstance(state, dict):
        index = _proxy_index(entries, state.get(_GOPAY_PROXY_STATE_KEY, ""))
        if index >= 0:
            return index
    return -1


def gopay_proxy_for_attempt(attempt: int, state: dict = None) -> tuple[str, int, int]:
    entries = _require_gopay_proxy_pool()
    if attempt > len(entries):
        raise GopayProxyPoolExhausted("GOPAY_PROXY_POOL exhausted before login methods succeeded")
    current_index = _state_proxy_index(state, entries)
    if current_index < 0:
        index = 0
    elif attempt <= 1:
        index = current_index
    else:
        index = (current_index + 1) % len(entries)
    proxy = entries[index]
    if isinstance(state, dict):
        state[_GOPAY_PROXY_STATE_KEY] = proxy
    return proxy, index + 1, len(entries)


def reset_gopay_proxy_rotation(state: dict) -> None:
    return None


def gopay_proxy_attempt_limit() -> int:
    return max(1, len(gopay_proxy_pool_entries()))


def gopay_proxy_for_state(state: dict) -> str:
    entries = _require_gopay_proxy_pool()
    index = _state_proxy_index(state, entries)
    if index < 0:
        proxy, _, _ = gopay_proxy_for_attempt(1, state)
        return proxy
    return entries[index]


def wait_otp(prompt: str = "Enter OTP: ") -> str:
    """等待 OTP：CLI 只支持手动输入；自动接码由 orchestrator 调用 SmsService。"""
    return input(prompt).strip()


def load_state():
    return {}


def save_state(state):
    return None


def _extract_recovery_token(data: dict) -> str:
    return (
        data.get("token")
        or data.get("recovery_token")
        or data.get("login_token")
        or data.get("grant_token")
        or data.get("code")
        or ""
    )


def _country_code(country_code: str = "") -> str:
    value = str(country_code or GOPAY_COUNTRY_CODE or "62").strip()
    return value if value.startswith("+") else f"+{value}"


def _normalize_phone(phone: str, country_code: str = "") -> str:
    prefix = _country_code(country_code).lstrip("+")
    value = str(phone or "").strip().lstrip("+")
    if value.startswith(prefix):
        value = value[len(prefix):]
    return value


def _auth_body(**extra) -> dict:
    body = dict(extra)
    body["client_id"] = GOTO_CLIENT_ID
    body["client_secret"] = GOTO_CLIENT_SECRET
    return body


def _signup_basic_authorization() -> str:
    return "Basic " + base64.b64encode(GOPAY_SIGNUP_AUTH_UUID.encode("utf-8")).decode("ascii")


def new_logon_device_profile() -> dict:
    """Create a fresh device profile for a new login/signup attempt."""
    device = generate_random_device_fingerprint()
    device["profile_id"] = os.urandom(8).hex()
    device["profile_created_at"] = int(time.time())
    return device


def ensure_state_device(state: dict) -> dict:
    """Return the persisted device profile for this account/login state."""
    device = state.get("device") if isinstance(state.get("device"), dict) else None
    if device is None:
        device = new_logon_device_profile()
        state["device"] = device
        save_state(state)
        return device

    before = json.dumps(device, sort_keys=True, default=str)
    ensure_device_fingerprint(device)
    if not device.get("profile_id"):
        device["profile_id"] = os.urandom(8).hex()
    if not device.get("profile_created_at"):
        device["profile_created_at"] = int(time.time())
    if json.dumps(device, sort_keys=True, default=str) != before:
        state["device"] = device
        save_state(state)
    return device


def _otp_method_from_channel(value: str = "") -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"sms", "otp_sms"}:
        return "otp_sms"
    if normalized in {"wa", "whatsapp", "otp_wa"}:
        return "otp_wa"
    return ""


def _available_otp_methods(methods) -> list:
    return [str(method).strip() for method in (methods or []) if str(method).strip()]


def _choose_otp_method(methods, preferred: str = "", default_method: str = "") -> str:
    explicit = _otp_method_from_channel(preferred)
    available = _available_otp_methods(methods)
    if str(preferred or "").strip() and not explicit:
        return ""
    if explicit:
        if available and explicit not in available:
            return ""
        return explicit
    default_method = _otp_method_from_channel(default_method) or _otp_method_from_channel(GOPAY_LOGIN_OTP_METHOD) or "otp_wa"
    fallbacks = (default_method, "otp_sms", "otp_wa") if default_method == "otp_sms" else (default_method, "otp_wa", "otp_sms")
    for method in fallbacks:
        if method and (not available or method in available):
            return method
    return default_method


def _choose_method(methods, preferred: str = "") -> str:
    return _choose_otp_method(methods, preferred, default_method="otp_sms")


def _otp_method_unavailable(methods, requested: str) -> str:
    method = _otp_method_from_channel(requested)
    if method:
        return f"{method} unavailable: {_available_otp_methods(methods)}"
    return f"otp method unavailable: {_available_otp_methods(methods)}"


def _response_error(label: str, response: dict) -> str:
    raw = response.get("raw")
    detail = raw if raw is not None else response.get("data")
    return f"{label}: status {response.get('status')} {detail}"


def _is_rate_limited(response: dict) -> bool:
    if response.get("status") == 429:
        return True
    raw = response.get("raw") if isinstance(response.get("raw"), dict) else {}
    data = response.get("data") if isinstance(response.get("data"), dict) else {}
    errors = raw.get("errors") or data.get("errors") or []
    for err in errors:
        if isinstance(err, dict) and "ratelimited" in str(err.get("code", "")).lower():
            return True
    return False


def login_methods_invalid_user(response: dict) -> bool:
    if response.get("status") != 401:
        return False
    raw = response.get("raw") if isinstance(response.get("raw"), dict) else {}
    data = response.get("data") if isinstance(response.get("data"), dict) else {}
    errors = raw.get("errors") or data.get("errors") or []
    for err in errors:
        if not isinstance(err, dict):
            continue
        text = " ".join(str(err.get(key, "")) for key in ("code", "message", "message_title")).lower()
        if "invalid user" in text or "could not find the user" in text:
            return True
    return False


def check_phone_by_login_methods(phone: str, country_code: str = "") -> dict:
    cc = _country_code(country_code)
    normalized_phone = _normalize_phone(phone, cc)
    attempts = gopay_proxy_attempt_limit()
    fingerprint_rotations = 0
    proxy_rotations = 0
    proxy_state = {}
    for attempt in range(1, attempts + 1):
        try:
            proxy, proxy_index, proxy_count = gopay_proxy_for_attempt(attempt, proxy_state)
        except GopayProxyPoolExhausted as exc:
            return {
                "success": False,
                "available": False,
                "status": "rate_limited",
                "error": str(exc),
                "attempts": attempt - 1,
                "fingerprint_rotations": fingerprint_rotations,
                "proxy_rotations": proxy_rotations,
                "proxy_pool_size": len(gopay_proxy_pool_entries()),
            }
        device = new_logon_device_profile()
        c = GopayClient("", proxy=proxy, device=device)
        r = c.post(f"{GOTO_AUTH}/goto-auth/login/methods", body=_auth_body(
            country_code=cc,
            device_verification_token_id="",
            email="",
            phone_number=normalized_phone,
        ))
        if r["status"] in (200, 201):
            data = r.get("data") if isinstance(r.get("data"), dict) else {}
            return {
                "success": True,
                "available": False,
                "status": "registered",
                "methods": data.get("methods") or [],
                "attempts": attempt,
                "fingerprint_rotations": fingerprint_rotations,
                "proxy_rotations": proxy_rotations,
                "proxy_pool_size": proxy_count,
            }
        if login_methods_invalid_user(r):
            return {
                "success": True,
                "available": True,
                "status": "available",
                "attempts": attempt,
                "fingerprint_rotations": fingerprint_rotations,
                "proxy_rotations": proxy_rotations,
                "proxy_pool_size": proxy_count,
            }
        if _is_rate_limited(r) and attempt < attempts:
            fingerprint_rotations += 1
            if proxy_count > 0:
                proxy_rotations += 1
            print(
                "[gopay-app] check phone rate limited; rotating fingerprint/proxy "
                f"attempt={attempt}/{attempts} profile_id={device.get('profile_id', '')} "
                f"proxy_index={proxy_index}/{proxy_count}",
                flush=True,
            )
            time.sleep(1)
            continue
        if _is_rate_limited(r):
            error = "GOPAY_PROXY_POOL exhausted before login methods succeeded"
            if attempt < proxy_count:
                error = _response_error("login methods rate limited", r)
            return {
                "success": False,
                "available": False,
                "status": "rate_limited",
                "error": error,
                "attempts": attempt,
                "fingerprint_rotations": fingerprint_rotations,
                "proxy_rotations": proxy_rotations,
                "proxy_pool_size": proxy_count,
            }
        return {
            "success": False,
            "available": False,
            "status": "error",
            "error": _response_error("login methods failed", r),
            "attempts": attempt,
            "fingerprint_rotations": fingerprint_rotations,
            "proxy_rotations": proxy_rotations,
            "proxy_pool_size": proxy_count,
        }
    return {
        "success": False,
        "available": False,
        "status": "error",
        "error": "login methods attempts exhausted",
    }


def _int_state(value, default: int = 0) -> int:
    try:
        return int(value or default)
    except (TypeError, ValueError):
        return default


def _pending_expired(state: dict, stage: str, sent_key: str, expires_key: str, timeout_seconds: int, now: int = None) -> bool:
    if state.get("stage") != stage:
        return False
    now = int(time.time()) if now is None else now
    expires_at = _int_state(state.get(expires_key))
    if expires_at:
        return now >= expires_at
    sent_at = _int_state(state.get(sent_key))
    if sent_at:
        return now >= sent_at + timeout_seconds
    return True


def login_pending_expired(state: dict, now: int = None) -> bool:
    if state.get("stage") != "login_otp_pending":
        return False
    now = int(time.time()) if now is None else now
    expires_at = _int_state(state.get("_login_otp_expires_at"))
    if expires_at:
        return now >= expires_at
    sent_at = _int_state(state.get("_login_otp_sent_at"))
    if sent_at:
        return now >= sent_at + GOPAY_OTP_TIMEOUT_SECONDS
    return True


def clear_login_state(state: dict, reason: str = "") -> None:
    for key in LOGIN_STATE_KEYS:
        state.pop(key, None)
    if state.get("stage") in ("login", "login_otp_pending"):
        state["stage"] = "deactivated" if state.get("deactivated_at") else "idle"
    if reason:
        state["last_error"] = reason


def expire_login_if_needed(state: dict, now: int = None) -> bool:
    if not login_pending_expired(state, now=now):
        return False
    clear_login_state(state, "LOGIN_OTP_TIMEOUT")
    return True


def signup_pending_expired(state: dict, now: int = None) -> bool:
    return _pending_expired(
        state,
        "signup_otp_pending",
        "_signup_otp_sent_at",
        "_signup_otp_expires_at",
        GOPAY_OTP_TIMEOUT_SECONDS,
        now=now,
    )


def signup_pin_pending_expired(state: dict, now: int = None) -> bool:
    return _pending_expired(
        state,
        "signup_pin_otp_pending",
        "_signup_pin_otp_sent_at",
        "_signup_pin_otp_expires_at",
        GOPAY_OTP_TIMEOUT_SECONDS,
        now=now,
    )


def clear_signup_state(state: dict, reason: str = "") -> None:
    for key in SIGNUP_STATE_KEYS:
        state.pop(key, None)
    if state.get("stage") in ("signup", "signup_otp_pending", "signup_pin_required", "signup_pin_otp_pending"):
        state["stage"] = "deactivated" if state.get("deactivated_at") and not state.get("token") else "idle"
    if reason:
        state["last_error"] = reason


def clear_signup_otp_state(state: dict, reason: str = "") -> None:
    for key in SIGNUP_OTP_STATE_KEYS:
        state.pop(key, None)
    if state.get("stage") in ("signup", "signup_otp_pending"):
        state["stage"] = "deactivated" if state.get("deactivated_at") and not state.get("token") else "idle"
    if reason:
        state["last_error"] = reason


def clear_signup_pin_state(state: dict, reason: str = "") -> None:
    for key in SIGNUP_PIN_STATE_KEYS:
        state.pop(key, None)
    if state.get("stage") == "signup_pin_otp_pending":
        state["stage"] = "signup_pin_required" if state.get("token") else "idle"
    if reason:
        state["last_error"] = reason


def expire_signup_if_needed(state: dict, now: int = None) -> bool:
    if signup_pending_expired(state, now=now):
        clear_signup_otp_state(state, "SIGNUP_OTP_TIMEOUT")
        return True
    if signup_pin_pending_expired(state, now=now):
        clear_signup_pin_state(state, "SIGNUP_PIN_OTP_TIMEOUT")
        return True
    return False


def _decode_jwt_payload(token: str) -> dict:
    token = str(token or "").strip().removeprefix("Bearer ").strip()
    parts = token.split(".")
    if len(parts) < 2:
        return {}
    payload = parts[1]
    payload += "=" * (-len(payload) % 4)
    try:
        return json.loads(base64.urlsafe_b64decode(payload.encode()).decode("utf-8"))
    except Exception:
        return {}


def access_token_expires_at(token_or_state) -> int:
    token = token_or_state.get("token", "") if isinstance(token_or_state, dict) else token_or_state
    payload = _decode_jwt_payload(token)
    try:
        return int(payload.get("exp") or 0)
    except (TypeError, ValueError):
        return 0


def access_token_usable(state: dict, min_ttl_seconds: int = 30) -> bool:
    token = str(state.get("token", "")).strip()
    if not token:
        return False
    expires_at = access_token_expires_at(token)
    if not expires_at:
        return True
    return expires_at > int(time.time()) + min_ttl_seconds


def _store_token_response(state: dict, data: dict) -> None:
    token = str(data.get("access_token") or "").strip()
    if not token:
        raise RuntimeError("access_token missing")
    state["token"] = token
    if data.get("refresh_token"):
        state["refresh_token"] = str(data.get("refresh_token") or "").strip()
    elif not data.get("_preserve_refresh_token"):
        state.pop("refresh_token", None)
    expires_at = access_token_expires_at(token)
    if not expires_at:
        try:
            expires_in = int(data.get("expires_in") or 0)
        except (TypeError, ValueError):
            expires_in = 0
        if expires_in > 0:
            expires_at = int(time.time()) + expires_in
    if expires_at:
        state["token_expires_at"] = expires_at
    else:
        state.pop("token_expires_at", None)
    state.pop("last_token_refresh_error", None)
    state.pop("last_token_refresh_failed_at", None)


def persist_login_start_state(state: dict, device: dict, phone: str) -> None:
    state["device"] = device
    state["_login_phone"] = phone
    state["_login_started_at"] = int(time.time())
    state["stage"] = "login"
    state.pop("last_error", None)
    save_state(state)


def persist_login_ready_state(state: dict, token_data: dict, phone: str) -> None:
    _store_token_response(state, token_data)
    state["phone"] = phone
    state["stage"] = "ready"
    state["ready_at"] = int(time.time())
    state.pop("last_error", None)
    for key in LOGIN_STATE_KEYS:
        state.pop(key, None)
    save_state(state)


def persist_login_otp_state(
    state: dict,
    phone: str,
    country_code: str,
    verification_id: str,
    method: str,
    otp_token: str,
    two_fa_token: str,
) -> None:
    state["_login_phone"] = phone
    state["_login_country_code"] = country_code
    state["_login_verification_id"] = verification_id
    state["_login_verification_method"] = method
    state["_login_otp_token"] = otp_token
    state["_login_2fa_token"] = two_fa_token
    now = int(time.time())
    state["_login_otp_sent_at"] = now
    state["_login_otp_expires_at"] = now + GOPAY_OTP_TIMEOUT_SECONDS
    state["stage"] = "login_otp_pending"
    state.pop("last_error", None)
    save_state(state)


def persist_signup_start_state(state: dict, device: dict, phone: str, country_code: str, name: str, email: str) -> None:
    state["device"] = device
    state["_signup_phone"] = phone
    state["_signup_country_code"] = country_code
    state["_signup_name"] = name
    state["_signup_email"] = email
    state["_signup_started_at"] = int(time.time())
    state["stage"] = "signup"
    state.pop("last_error", None)
    save_state(state)


def persist_signup_otp_state(state: dict, verification_id: str, method: str, otp_token: str) -> None:
    now = int(time.time())
    state["_signup_verification_id"] = verification_id
    state["_signup_verification_method"] = method
    state["_signup_otp_token"] = otp_token
    state["_signup_otp_sent_at"] = now
    state["_signup_otp_expires_at"] = now + GOPAY_OTP_TIMEOUT_SECONDS
    state["stage"] = "signup_otp_pending"
    state.pop("last_error", None)
    save_state(state)


def persist_signup_otp_retry_state(state: dict, otp_token: str = "") -> None:
    if otp_token:
        state["_signup_otp_token"] = otp_token
    now = int(time.time())
    state["_signup_otp_sent_at"] = now
    state["_signup_otp_expires_at"] = now + GOPAY_OTP_TIMEOUT_SECONDS
    state["stage"] = "signup_otp_pending"
    state.pop("last_error", None)
    save_state(state)


def persist_signup_complete_state(state: dict, token_data: dict, phone: str, name: str, email: str) -> None:
    _store_token_response(state, token_data)
    state["phone"] = phone
    state["name"] = name
    state["email"] = email
    state["stage"] = "signup_pin_required"
    state.pop("last_error", None)
    for key in SIGNUP_OTP_STATE_KEYS:
        state.pop(key, None)
    save_state(state)


def migrate_active_tokens_to_tmp(state: dict, phone: str = "") -> bool:
    moved = False
    for key in ACTIVE_TOKEN_STATE_KEYS:
        if key not in state:
            continue
        value = state.pop(key)
        if value not in ("", None):
            state[f"_tmp_{key}"] = value
            moved = True
    for key in ACTIVE_TOKEN_METADATA_KEYS:
        state.pop(key, None)
    if moved:
        state["_tmp_token_migrated_at"] = int(time.time())
        if phone:
            state["_tmp_phone"] = phone
    return moved


def clear_tmp_tokens(state: dict) -> None:
    for key in TMP_TOKEN_STATE_KEYS + TMP_TOKEN_METADATA_KEYS:
        state.pop(key, None)


def refresh_access_token(state: dict) -> dict:
    refresh_token = str(state.get("refresh_token") or "").strip()
    if not refresh_token:
        return {"success": False, "error": "refresh_token missing"}

    device = ensure_state_device(state)
    c = GopayClient(str(state.get("token") or "").strip(), proxy=gopay_proxy_for_state(state), device=device)
    candidates = [
        _auth_body(grant_type="refresh_token", token=refresh_token),
        _auth_body(grant_type="refresh_token", refresh_token=refresh_token),
    ]
    last_response = None
    for body in candidates:
        r = c.post(f"{GOTO_AUTH}/goto-auth/token", body=body)
        last_response = r
        data = r.get("data") if isinstance(r, dict) else {}
        if r.get("status") in (200, 201) and isinstance(data, dict) and data.get("access_token"):
            data = dict(data)
            data["_preserve_refresh_token"] = True
            _store_token_response(state, data)
            state["last_token_refresh_at"] = int(time.time())
            state.pop("last_token_refresh_error", None)
            if state.get("last_error") == "TOKEN_REFRESH_FAILED":
                state.pop("last_error", None)
            save_state(state)
            return {
                "success": True,
                "refreshed": True,
                "expires_at": state.get("token_expires_at", 0),
            }

    error = _response_error("refresh token failed", last_response or {"status": 0, "data": {}})
    state["last_token_refresh_error"] = error
    state["last_token_refresh_failed_at"] = int(time.time())
    if not access_token_usable(state, 0):
        state["last_error"] = "TOKEN_REFRESH_FAILED"
    save_state(state)
    return {"success": False, "error": error}


def ensure_access_token(state: dict, min_ttl_seconds: int = None, force: bool = False) -> dict:
    min_ttl = GOPAY_TOKEN_REFRESH_MIN_TTL_SECONDS if min_ttl_seconds is None else min_ttl_seconds
    token = str(state.get("token", "")).strip()
    expires_at = access_token_expires_at(token)
    if expires_at:
        state["token_expires_at"] = expires_at
    if token and not force and access_token_usable(state, min_ttl):
        if expires_at:
            save_state(state)
        return {"success": True, "refreshed": False, "expires_at": expires_at}
    result = refresh_access_token(state)
    if result.get("success"):
        return result
    if token and access_token_usable(state, 0):
        return {
            "success": True,
            "refreshed": False,
            "expires_at": expires_at,
            "warning": result.get("error", ""),
        }
    return result


def _parse_balance_amount(value):
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value)
    text = str(value).strip()
    if not text:
        return None
    digits = "".join(ch for ch in text if ch.isdigit() or ch == "-")
    if not digits or digits == "-":
        return None
    try:
        return int(digits)
    except ValueError:
        return None


def _gopay_wallet_balance(data) -> tuple:
    items = data.get("data") if isinstance(data, dict) and isinstance(data.get("data"), list) else data
    if isinstance(items, dict):
        items = [items]
    if not isinstance(items, list):
        return None, ""

    for item in items:
        if not isinstance(item, dict) or item.get("type") != "GOPAY_WALLET":
            continue
        balance = item.get("balance") if isinstance(item.get("balance"), dict) else {}
        amount = _parse_balance_amount(balance.get("value"))
        if amount is None:
            amount = _parse_balance_amount(balance.get("display_value"))
        currency = str(balance.get("currency") or item.get("currency") or "").strip()
        return amount, currency
    return None, ""


def get_qr_id(state: dict) -> dict:
    token = str(state.get("token") or "").strip()
    if not token:
        return {"success": False, "error": "access_token missing"}
    device = ensure_state_device(state)
    c = GopayClient(token, proxy=gopay_proxy_for_state(state), device=device)
    r = c.get(f"{GOPAY_CUSTOMER}/v1/users/profile")
    if r.get("status") != 200:
        return {"success": False, "error": _response_error("users/profile failed", r)}
    data = r.get("data") if isinstance(r.get("data"), dict) else {}
    qr_id = str(data.get("qr_id") or "").strip()
    if not qr_id:
        raw = r.get("raw") if isinstance(r.get("raw"), dict) else r.get("data")
        return {"success": False, "error": f"qr_id not found in response: {json.dumps(raw, default=str)[:500]}"}
    return {"success": True, "qr_id": qr_id}


def check_gopay_balance(state: dict) -> dict:
    token = str(state.get("token") or "").strip()
    if not token:
        return {"success": False, "error": "access_token missing", "status": 0}

    device = ensure_state_device(state)
    c = GopayClient(token, proxy=gopay_proxy_for_state(state), device=device)
    r = c.get(f"{GOPAY_CUSTOMER}/v1/payment-options/balances")
    now = int(time.time())
    state["last_balance_check_at"] = now
    if r.get("status") != 200:
        error = _response_error("balance check failed", r)
        state["last_balance_error"] = error
        save_state(state)
        return {"success": False, "status": r.get("status", 0), "error": error}

    raw = r.get("raw") if isinstance(r.get("raw"), dict) else {}
    if raw.get("success") is False:
        error = _response_error("balance check failed", r)
        state["last_balance_error"] = error
        save_state(state)
        return {"success": False, "status": r.get("status", 0), "error": error}

    amount, currency = _gopay_wallet_balance(r.get("data"))
    if amount is None:
        error = "gopay wallet balance missing"
        state["last_balance_error"] = error
        save_state(state)
        return {"success": False, "status": r.get("status", 0), "error": error}

    has_min_balance = amount >= GOPAY_MIN_BALANCE_RP
    state["balance_amount"] = amount
    state["balance_currency"] = currency or "IDR"
    state["has_min_balance"] = has_min_balance
    state.pop("last_balance_error", None)
    if has_min_balance:
        if state.get("last_error") == "INSUFFICIENT_GOPAY_BALANCE":
            state.pop("last_error", None)
    else:
        state["last_error"] = "INSUFFICIENT_GOPAY_BALANCE"
    save_state(state)
    return {
        "success": True,
        "status": 200,
        "balance_amount": amount,
        "balance_currency": state["balance_currency"],
        "has_min_balance": has_min_balance,
    }


def verify_access_token(state: dict) -> dict:
    token = str(state.get("token") or "").strip()
    if not token:
        return {"success": False, "error": "access_token missing", "status": 0}
    device = ensure_state_device(state)
    c = GopayClient(token, proxy=gopay_proxy_for_state(state), device=device)
    r = c.get(f"{GOPAY_CUSTOMER}/v1/users/profile")
    if r.get("status") == 200:
        data = r.get("data") if isinstance(r.get("data"), dict) else {}
        phone = data.get("phone") or data.get("number") or ""
        if phone:
            state["phone"] = _normalize_phone(phone)
        state["stage"] = "ready"
        state["ready_at"] = int(time.time())
        state.pop("last_error", None)
        save_state(state)
        return {"success": True, "status": 200, "phone": state.get("phone", "")}
    return {"success": False, "status": r.get("status", 0), "error": _response_error("profile failed", r)}


def _token_valid_result(state: dict, profile: dict, refreshed: bool) -> dict:
    balance = check_gopay_balance(state)
    result = {
        "success": bool(balance.get("success")),
        "token_valid": True,
        "refreshed": refreshed,
        "phone": profile.get("phone", ""),
        "balance_amount": int(balance.get("balance_amount") or state.get("balance_amount") or 0),
        "balance_currency": balance.get("balance_currency") or state.get("balance_currency", ""),
        "has_min_balance": bool(balance.get("has_min_balance")),
    }
    if not balance.get("success"):
        result["error"] = balance.get("error", "balance check failed")
    return result


def check_token_valid(state: dict) -> dict:
    profile = verify_access_token(state)
    if profile.get("success"):
        return _token_valid_result(state, profile, False)

    refresh = refresh_access_token(state)
    if not refresh.get("success"):
        return {
            "success": False,
            "token_valid": False,
            "refreshed": False,
            "error": refresh.get("error") or profile.get("error", "token invalid"),
        }

    profile = verify_access_token(state)
    if profile.get("success"):
        return _token_valid_result(state, profile, True)
    return {
        "success": False,
        "token_valid": False,
        "refreshed": True,
        "error": profile.get("error", "profile failed after refresh"),
    }


def start_login(state: dict, phone: str, pin: str = "", country_code: str = "", otp_channel: str = "") -> dict:
    """Start GoTo login and stop at 2FA OTP if needed."""
    cc = _country_code(country_code)
    normalized_phone = _normalize_phone(phone, cc)
    attempts = gopay_proxy_attempt_limit()
    reset_gopay_proxy_rotation(state)
    for attempt in range(1, attempts + 1):
        try:
            proxy, proxy_index, proxy_count = gopay_proxy_for_attempt(attempt, state)
        except GopayProxyPoolExhausted as exc:
            return {"success": False, "error": str(exc)}
        device = new_logon_device_profile()
        persist_login_start_state(state, device, normalized_phone)

        c = GopayClient("", proxy=proxy, device=device)
        r = c.post(f"{GOTO_AUTH}/goto-auth/login/methods", body=_auth_body(
            country_code=cc,
            device_verification_token_id="",
            email="",
            phone_number=normalized_phone,
        ))
        if r["status"] in (200, 201):
            break
        if _is_rate_limited(r) and attempt < attempts:
            print(
                "[gopay-app] login methods rate limited; rotating fingerprint/proxy "
                f"attempt={attempt}/{attempts} proxy_index={proxy_index}/{proxy_count}",
                flush=True,
            )
            time.sleep(1)
            continue
        if _is_rate_limited(r):
            return {"success": False, "error": "GOPAY_PROXY_POOL exhausted before login methods succeeded"}
        if login_methods_invalid_user(r):
            return {"success": False, "not_registered": True, "error": _response_error("login methods failed", r)}
        return {"success": False, "error": _response_error("login methods failed", r)}

    methods = r["data"].get("methods", [])
    verification_id = r["data"].get("verification_id", "")
    if not verification_id:
        return {"success": False, "error": "verification_id missing"}
    if "goto_pin" not in methods:
        return {"success": False, "error": f"goto_pin unavailable: {methods}"}
    if not pin:
        return {"success": False, "error": "gopay pin missing"}

    r = c.post(
        f"{GOTO_AUTH}/cvs/v1/initiate",
        body=_auth_body(
            country_code=cc,
            device_verification_token_id=None,
            email_address=None,
            flow="login_1fa",
            is_multiple_method=True,
            phone_number=normalized_phone,
            verification_id=verification_id,
            verification_method="goto_pin",
        ),
        extra_headers={"Authorization": ""},
    )
    if r["status"] != 200:
        return {"success": False, "error": _response_error("login pin initiate failed", r)}

    challenge_id = r["data"].get("challenge_id", "")
    if not challenge_id:
        return {"success": False, "error": "pin challenge_id missing"}

    r = c.get(f"{GOPAY_CUSTOMER}/api/v2/challenges/{challenge_id}/pin-page/nb")
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin page failed", r)}

    r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pin/tokens/nb", body={
        "challenge_id": challenge_id,
        "client_id": GOPAY_PIN_CLIENT_ID,
        "pin": pin,
    })
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin token failed", r)}

    validation_jwt = r["data"].get("token", "")
    if not validation_jwt:
        return {"success": False, "error": "pin validation token missing"}

    r = c.post(f"{GOTO_AUTH}/cvs/v1/verify", body=_auth_body(
        data={"challenge_id": challenge_id, "validation_jwt": validation_jwt},
        flow="login_1fa",
        verification_id=verification_id,
        verification_method="goto_pin",
    ))
    if r["status"] != 200:
        return {"success": False, "error": _response_error("login pin verify failed", r)}

    verification_token = r["data"].get("verification_token", "")
    if not verification_token:
        return {"success": False, "error": "1fa verification_token missing"}

    r = c.post(
        f"{GOTO_AUTH}/goto-auth/accountlist",
        body=_auth_body(),
        extra_headers={"Verification-Token": f"Bearer {verification_token}"},
    )
    if r["status"] != 200:
        return {"success": False, "error": _response_error("accountlist failed", r)}

    accounts = r["data"].get("account_list", [])
    account_id = accounts[0].get("account_id", "") if accounts else ""
    one_fa_token = r["data"].get("1fa_token", "")
    if not account_id or not one_fa_token:
        return {"success": False, "error": "account_id or 1fa_token missing"}

    r = c.post(f"{GOTO_AUTH}/goto-auth/token", body=_auth_body(
        account_id=account_id,
        ext_user_token=None,
        grant_type="cvs",
        token=one_fa_token,
    ))
    if r["status"] == 201:
        persist_login_ready_state(state, r["data"], normalized_phone)
        return {"success": True, "ready": True, "otp_sent": False}

    two_fa_token = r["data"].get("2fa_token", "") if isinstance(r.get("data"), dict) else ""
    verification_id = r["data"].get("verification_id", "") if isinstance(r.get("data"), dict) else ""
    if r["status"] != 403 or not two_fa_token or not verification_id:
        return {"success": False, "error": _response_error("token exchange failed", r)}

    method = _choose_otp_method(r["data"].get("methods", []), otp_channel)
    if not method:
        return {"success": False, "error": _otp_method_unavailable(r["data"].get("methods", []), otp_channel)}
    r = c.post(
        f"{GOTO_AUTH}/cvs/v1/initiate",
        body=_auth_body(
            country_code=cc,
            device_verification_token_id=None,
            email_address=None,
            flow="login_2fa",
            is_multiple_method=None,
            phone_number=normalized_phone,
            verification_id=verification_id,
            verification_method=method,
        ),
        extra_headers={"Authorization": ""},
    )
    if r["status"] != 200:
        return {"success": False, "error": _response_error("2fa otp initiate failed", r)}

    otp_token = r["data"].get("otp_token", "")
    if not otp_token:
        return {"success": False, "error": "2fa otp_token missing"}

    persist_login_otp_state(state, normalized_phone, cc, verification_id, method, otp_token, two_fa_token)
    return {"success": True, "ready": False, "otp_sent": True, "verification_id": verification_id, "method": method}


def complete_login(state: dict, otp: str) -> str:
    device = ensure_state_device(state)
    c = GopayClient("", proxy=gopay_proxy_for_state(state), device=device)
    verification_id = state.get("_login_verification_id", "")
    otp_token = state.get("_login_otp_token", "")
    method = state.get("_login_verification_method", "otp_wa")
    two_fa_token = state.get("_login_2fa_token", "")
    if not verification_id or not otp_token or not two_fa_token:
        raise RuntimeError("login 2fa state missing")

    r = c.post(f"{GOTO_AUTH}/cvs/v1/verify", body=_auth_body(
        data={"otp": otp, "otp_token": otp_token},
        flow="login_2fa",
        verification_id=verification_id,
        verification_method=method,
    ))
    if r["status"] != 200:
        raise RuntimeError(_response_error("2fa verify failed", r))
    verification_token = r["data"].get("verification_token", "")
    if not verification_token:
        raise RuntimeError("2fa verification_token missing")

    r = c.post(
        f"{GOTO_AUTH}/goto-auth/token",
        body=_auth_body(
            ext_user_token=None,
            grant_type="challenge",
            token=two_fa_token,
        ),
        extra_headers={"Verification-Token": f"Bearer {verification_token}"},
    )
    if r["status"] != 201:
        raise RuntimeError(_response_error("challenge token failed", r))

    persist_login_ready_state(state, r["data"], state.get("_login_phone", ""))
    return state["token"]


def start_signup(state: dict, phone: str, name: str, email: str, country_code: str = "", otp_channel: str = "") -> dict:
    cc = _country_code(country_code)
    normalized_phone = _normalize_phone(phone, cc)
    name = str(name or "").strip()
    email = str(email or "").strip()
    if not normalized_phone:
        return {"success": False, "error": "signup phone missing"}
    if not name:
        return {"success": False, "error": "signup name missing"}

    clear_signup_state(state)
    clear_login_state(state)
    device = new_logon_device_profile()
    persist_signup_start_state(state, device, normalized_phone, cc, name, email)

    proxy = gopay_proxy_for_state(state)
    c = GopayClient(state.get("token", ""), proxy=proxy, device=device)
    r = c.post(f"{GOTO_AUTH}/cvs/v1/methods", body=_auth_body(
        country_code=cc,
        device_verification_token_id=None,
        email_address=None,
        flow="signup",
        phone_number=normalized_phone,
    ))
    log_api_response("signup methods response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("signup methods failed", r), "raw_json": safe_response_json(r)}

    verification_id = r["data"].get("verification_id", "")
    if not verification_id:
        return {"success": False, "error": "signup verification_id missing", "raw_json": safe_response_json(r)}
    method = _choose_method(r["data"].get("methods", []), otp_channel)
    if not method:
        return {"success": False, "error": _otp_method_unavailable(r["data"].get("methods", []), otp_channel), "raw_json": safe_response_json(r)}

    r = c.post(f"{GOTO_AUTH}/cvs/v1/initiate", body=_auth_body(
        country_code=cc,
        device_verification_token_id=None,
        email_address=None,
        flow="signup",
        is_multiple_method=None,
        phone_number=normalized_phone,
        verification_id=verification_id,
        verification_method=method,
    ))
    log_api_response("signup otp initiate response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("signup otp initiate failed", r), "raw_json": safe_response_json(r)}

    otp_token = r["data"].get("otp_token", "")
    if not otp_token:
        return {"success": False, "error": "signup otp_token missing", "raw_json": safe_response_json(r)}

    persist_signup_otp_state(state, verification_id, method, otp_token)
    retry_timers = r.get("data", {}).get("retry_timer_in_seconds") if isinstance(r.get("data"), dict) else []
    return {
        "success": True,
        "otp_sent": True,
        "verification_id": verification_id,
        "method": method,
        "retry_timer_seconds": retry_timers if isinstance(retry_timers, list) else [],
        "raw_json": safe_response_json(r),
    }


def retry_signup_otp(state: dict) -> dict:
    if state.get("stage") != "signup_otp_pending":
        return {"success": False, "error": f"not waiting for signup otp: {state.get('stage', 'idle')}"}
    otp_token = state.get("_signup_otp_token", "")
    method = state.get("_signup_verification_method", "otp_sms")
    if not otp_token:
        return {"success": False, "error": "signup otp state missing"}

    c = GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))
    r = c.post(f"{GOTO_AUTH}/cvs/v1/retry", body=_auth_body(
        flow="signup",
        verification_method=method,
        data={"otp_token": otp_token},
    ))
    log_api_response("signup otp retry response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("signup otp retry failed", r), "raw_json": safe_response_json(r)}
    data = r.get("data") if isinstance(r.get("data"), dict) else {}
    persist_signup_otp_retry_state(state, data.get("otp_token", ""))
    return {"success": True, "otp_sent": True, "raw_json": safe_response_json(r)}


def complete_signup(state: dict, otp: str) -> dict:
    if state.get("stage") != "signup_otp_pending":
        return {"success": False, "error": f"not waiting for signup otp: {state.get('stage', 'idle')}"}
    otp = str(otp or "").strip()
    if not otp:
        return {"success": False, "error": "signup otp required"}

    phone = state.get("_signup_phone", "")
    cc = state.get("_signup_country_code", _country_code())
    name = state.get("_signup_name", "")
    email = state.get("_signup_email", "")
    verification_id = state.get("_signup_verification_id", "")
    method = state.get("_signup_verification_method", "otp_sms")
    otp_token = state.get("_signup_otp_token", "")
    if not phone or not verification_id or not otp_token:
        return {"success": False, "error": "signup otp state missing"}

    c = GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))
    r = c.post(f"{GOTO_AUTH}/cvs/v1/verify", body=_auth_body(
        data={"otp": otp, "otp_token": otp_token},
        flow="signup",
        verification_id=verification_id,
        verification_method=method,
    ))
    log_api_response("signup otp verify response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("signup otp verify failed", r), "raw_json": safe_response_json(r)}
    verification_token = r["data"].get("verification_token", "")
    if not verification_token:
        return {"success": False, "error": "signup verification_token missing", "raw_json": safe_response_json(r)}
    signup_body = {
        "client_name": GOTO_CLIENT_ID,
        "client_secret": GOTO_CLIENT_SECRET,
        "data": {
            "name": name,
            "phone": f"{cc}{phone}",
            "email": email,
            "signed_up_country": cc,
            "onboarding_partner": "gopay_consumer_app",
        },
    }
    log_api_response("customer signup request", {"body": signup_body})
    r = c.post(
        f"{GOJEK_API}/v7/customers/signup",
        body=signup_body,
        extra_headers={
            "Authorization": _signup_basic_authorization(),
            "Verification-Token": f"Bearer {verification_token}",
        },
    )
    log_api_response("customer signup response", r)
    if r["status"] != 201:
        return {"success": False, "error": _response_error("customer signup failed", r), "raw_json": safe_response_json(r)}

    persist_signup_complete_state(state, r["data"], phone, name, email)
    refresh = ensure_access_token(state, force=True)
    if not refresh.get("success"):
        state["last_error"] = refresh.get("error", "signup token refresh failed")
        save_state(state)
        return {"success": False, "error": state["last_error"], "raw_json": safe_response_json(r)}
    state["stage"] = "signup_pin_required"
    save_state(state)
    return {"success": True, "phone": phone, "pin_setup_required": True, "raw_json": safe_response_json(r)}


def start_signup_pin(state: dict, pin: str, otp_channel: str = "") -> dict:
    pin = str(pin or "").strip()
    if not pin:
        return {"success": False, "error": "gopay pin missing"}
    refresh = ensure_access_token(state)
    if not refresh.get("success") and not access_token_usable(state, 0):
        return {"success": False, "error": refresh.get("error", "token refresh failed")}

    phone = state.get("_signup_phone") or state.get("phone", "")
    if not phone:
        return {"success": False, "error": "signup phone missing"}

    c = GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))
    profile = c.get(f"{GOPAY_CUSTOMER}/v1/users/profile")
    if profile.get("status") == 200:
        data = profile.get("data") if isinstance(profile.get("data"), dict) else {}
        if data.get("is_pin_setup"):
            phone = data.get("phone") or data.get("number") or phone
            state["phone"] = _normalize_phone(phone)
            state["stage"] = "ready"
            state["pin_setup_at"] = int(time.time())
            state["ready_at"] = int(time.time())
            state.pop("last_error", None)
            for key in SIGNUP_STATE_KEYS:
                state.pop(key, None)
            save_state(state)
            return {"success": True, "phone": state.get("phone", ""), "pin_setup_complete": True}

    r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pins/allowed", body={"pin": pin})
    log_api_response("pin allowed response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin allowed failed", r)}

    r = c.post(f"{GOTO_AUTH}/cvs/v1/methods", body=_auth_body(
        country_code=None,
        device_verification_token_id=None,
        email_address=None,
        flow="goto_pin_wa_sms",
        phone_number=None,
    ))
    log_api_response("pin otp methods response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin otp methods failed", r)}

    verification_id = r["data"].get("verification_id", "")
    if not verification_id:
        return {"success": False, "error": "pin verification_id missing"}
    method = _choose_method(r["data"].get("methods", []), otp_channel)
    if not method:
        return {"success": False, "error": _otp_method_unavailable(r["data"].get("methods", []), otp_channel)}

    r = c.post(f"{GOTO_AUTH}/cvs/v1/initiate", body=_auth_body(
        country_code=None,
        device_verification_token_id=None,
        email_address=None,
        flow="goto_pin_wa_sms",
        is_multiple_method=None,
        phone_number=None,
        verification_id=verification_id,
        verification_method=method,
    ))
    log_api_response("pin otp initiate response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin otp initiate failed", r)}

    otp_token = r["data"].get("otp_token", "")
    if not otp_token:
        return {"success": False, "error": "pin otp_token missing"}

    now = int(time.time())
    state["_signup_pin_challenge_id"] = ""
    state["_signup_pin_client_id"] = ""
    state["_signup_pin_verification_id"] = verification_id
    state["_signup_pin_verification_method"] = method
    state["_signup_pin_otp_token"] = otp_token
    state["_signup_pin_otp_sent_at"] = now
    state["_signup_pin_otp_expires_at"] = now + GOPAY_OTP_TIMEOUT_SECONDS
    state["stage"] = "signup_pin_otp_pending"
    state.pop("last_error", None)
    save_state(state)
    return {
        "success": True,
        "otp_sent": True,
        "verification_id": verification_id,
        "method": method,
    }


def retry_signup_pin_otp(state: dict) -> dict:
    if state.get("stage") != "signup_pin_otp_pending":
        return {"success": False, "error": f"not waiting for signup pin otp: {state.get('stage', 'idle')}"}
    otp_token = state.get("_signup_pin_otp_token", "")
    method = state.get("_signup_pin_verification_method", "otp_sms")
    if not otp_token:
        return {"success": False, "error": "signup pin otp state missing"}

    refresh = ensure_access_token(state)
    if not refresh.get("success") and not access_token_usable(state, 0):
        return {"success": False, "error": refresh.get("error", "token refresh failed")}

    c = GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))
    r = c.post(f"{GOTO_AUTH}/cvs/v1/retry", body=_auth_body(
        flow="goto_pin_wa_sms",
        verification_method=method,
        data={"otp_token": otp_token},
    ))
    log_api_response("pin otp retry response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin otp retry failed", r)}
    data = r.get("data") if isinstance(r.get("data"), dict) else {}
    new_otp_token = data.get("otp_token", "")
    if not new_otp_token:
        return {"success": False, "error": "pin retry otp_token missing"}

    now = int(time.time())
    state["_signup_pin_otp_token"] = new_otp_token
    state["_signup_pin_otp_sent_at"] = now
    state["_signup_pin_otp_expires_at"] = now + GOPAY_OTP_TIMEOUT_SECONDS
    state.pop("last_error", None)
    save_state(state)
    return {"success": True, "otp_sent": True}


def complete_signup_pin(state: dict, otp: str, pin: str) -> dict:
    if state.get("stage") != "signup_pin_otp_pending":
        return {"success": False, "error": f"not waiting for signup pin otp: {state.get('stage', 'idle')}"}
    otp = str(otp or "").strip()
    pin = str(pin or "").strip()
    if not otp:
        return {"success": False, "error": "signup pin otp required"}
    if not pin:
        return {"success": False, "error": "gopay pin missing"}

    refresh = ensure_access_token(state)
    if not refresh.get("success") and not access_token_usable(state, 0):
        return {"success": False, "error": refresh.get("error", "token refresh failed")}

    verification_id = state.get("_signup_pin_verification_id", "")
    method = state.get("_signup_pin_verification_method", "otp_sms")
    otp_token = state.get("_signup_pin_otp_token", "")
    challenge_id = state.get("_signup_pin_challenge_id", "")
    client_id = state.get("_signup_pin_client_id", "")
    if not verification_id or not otp_token:
        return {"success": False, "error": "signup pin otp state missing"}

    c = GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))
    r = c.post(f"{GOTO_AUTH}/cvs/v1/verify", body=_auth_body(
        data={"otp": otp, "otp_token": otp_token},
        flow="goto_pin_wa_sms",
        verification_id=verification_id,
        verification_method=method,
    ))
    log_api_response("pin otp verify response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin otp verify failed", r)}
    verification_token = r["data"].get("verification_token", "")
    if not verification_token:
        return {"success": False, "error": "pin verification_token missing"}

    r = c.post(
        f"{GOPAY_CUSTOMER}/api/v2/users/pins/setup/tokens",
        body={"client_id": client_id, "pin": pin, "challenge_id": challenge_id},
        extra_headers={
            "Verification-Token": f"Bearer {verification_token}",
            "Is-Token-Required": "false",
        },
    )
    log_api_response("pin setup response", r)
    if r["status"] != 200:
        return {"success": False, "error": _response_error("pin setup failed", r)}

    phone = state.get("_signup_phone") or state.get("phone", "")
    state["phone"] = phone
    state["stage"] = "ready"
    state["pin_setup_at"] = int(time.time())
    state["ready_at"] = int(time.time())
    state.pop("last_error", None)
    for key in SIGNUP_STATE_KEYS:
        state.pop(key, None)
    save_state(state)
    return {"success": True, "phone": phone, "pin_setup_complete": True}


def _redacted_state(state: dict) -> dict:
    redacted = dict(state)
    for key, value in list(redacted.items()):
        if "token" in key and value:
            redacted[key] = "<redacted>"
    device = redacted.get("device")
    if isinstance(device, dict):
        device = dict(device)
        for key in ("D1", "x-session-id", "x-m1"):
            if device.get(key):
                device[key] = "<redacted>"
        redacted["device"] = device
    return redacted


def get_client(state) -> GopayClient:
    result = ensure_access_token(state)
    token = state.get("token", "")
    if not token or not result.get("success"):
        print("ERROR: No token. Run --step login first.")
        sys.exit(1)
    device = ensure_state_device(state)
    return GopayClient(token, proxy=gopay_proxy_for_state(state), device=device)


# === 改手机号 ===

def change_phone(state, new_phone: str, pin: str):
    """改手机号：3步。自动取号由 orchestrator + SmsService 负责。"""
    c = get_client(state)
    email = state.get("email", "")
    name = state.get("name", "")
    if not new_phone:
        raise RuntimeError("new_phone required; acquire temporary numbers through orchestrator SmsService")

    body = {"email": email, "name": name, "phone": f"+62{new_phone}", "profile_image_url": None}

    # Step 1: 触发 PIN 验证
    print("[1/3] Submitting phone change request...")
    r = c.patch(f"{GOJEK_API}/v5/customers", body=body)
    if r["status"] == 461:
        print("  → PIN required (expected)")
    elif r["status"] == 429:
        raise RuntimeError(f"Rate limited (429): {r['data']}")
    else:
        raise RuntimeError(f"Step 1 unexpected ({r['status']}): {r['data']}")

    # Step 2: 带 PIN 重新提交
    print(f"[2/3] Submitting with PIN...")
    r = c.patch(f"{GOJEK_API}/v5/customers", body=body, extra_headers={"pin": pin})
    if r["status"] == 200:
        print(f"  → OTP sent to +62{new_phone}")
    elif r["status"] == 429:
        raise RuntimeError(f"Rate limited (429): {r['data']}")
    else:
        raise RuntimeError(f"Step 2 failed ({r['status']}): {r['data']}")

    # Step 3: 等待 OTP
    otp = wait_otp("Enter OTP received on new phone: ")
    otp_token = r["data"].get("otp_token", "")

    r = c.post(f"{GOJEK_API}/v5/customers/verificationUpdateProfile", body={"otp": otp, "otp_token": otp_token})
    if r["status"] == 200:
        if GOPAY_CHANGE_PHONE_COUNTRY_SYNC:
            print("  Syncing GoPay country state...")
            sync = c.put(f"{GOPAY_API}/customers/v1/country-change")
            if sync["status"] != 200:
                raise RuntimeError(f"GoPay country-change sync failed ({sync['status']}): {sync['data']}")
        print("  → Phone changed successfully!")
        state["phone"] = new_phone
        save_state(state)
    else:
        raise RuntimeError(f"OTP verification failed ({r['status']}): {r['data']}")


# === 注销 ===

def deactivate(state, pin: str):
    """注销 GoPay 账户"""
    c = get_client(state)

    # Step 1: PIN challenge
    print("[1/3] Creating PIN challenge...")
    r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pin/challenges", body={"flow": "deactivation"})
    if r["status"] != 200:
        raise RuntimeError(f"PIN challenge failed: {r}")
    challenge_id = r["data"].get("challenge_id", "")
    client_id = r["data"].get("client_id", "")

    # Step 2: Verify PIN
    print("[2/3] Verifying PIN...")
    r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pin/tokens", body={
        "challenge_id": challenge_id, "client_id": client_id, "pin": pin
    })
    if r["status"] != 200:
        raise RuntimeError(f"PIN verification failed: {r}")

    # Step 3: Check eligibility
    print("  Checking deactivation eligibility...")
    r = c.get(f"{GOPAY_API}/api/v1/users/deactivate/check")
    print(f"  → Status: {r['status']}")

    # Step 4: Delete with OTP
    otp = wait_otp("Enter OTP received on phone: ")
    r = c.delete(f"{GOPAY_CUSTOMER}/api/v1/users/deactivate", body={
        "otp": otp, "reason": "I no longer need this account", "description": None
    })
    if r["status"] == 200:
        print("  → Account deactivated!")
        state["deactivated_at"] = int(time.time())
        save_state(state)
    elif r["status"] == 429:
        raise RuntimeError("Deactivation rate limited (429)")
    else:
        raise RuntimeError(f"Deactivation failed ({r['status']}): {r['data']}")


# === 登录 (GoTo SSO) ===

def login(state, phone: str, pin: str = ""):
    """通过 GoTo SSO 登录，使用新设备指纹"""
    print("[1/3] Starting GoTo login...")
    result = start_login(state, phone, pin or GOPAY_PIN)
    device = state.get("device", {})
    if device:
        print(f"  → fingerprint: {device.get('x-phonemake')} {device.get('x-phonemodel')} uid={device.get('x-uniqueid')}")
    if not result.get("success"):
        print(f"  → Failed: {result.get('error')}")
        return
    if result.get("ready"):
        print("  → Logged in! Token saved.")
        return

    print(f"[2/3] OTP sent via {result.get('method', 'otp')}.")
    otp = wait_otp("Enter OTP: ")
    print("[3/3] Verifying OTP and getting access token...")
    try:
        complete_login(state, otp)
    except Exception as e:
        print(f"  → Failed: {e}")
        return
    print("  → Logged in! Token saved.")


# === 解绑 ===

def unlink(state):
    """解绑所有 linked apps"""
    c = get_client(state)

    result = run_linked_app_unlink(c, LinkedAppUnlinkOptions())
    for step in result.get("steps", []):
        print(f"  {step.get('label')}: {step.get('status_code')}")
    if not result.get("success"):
        print(f"  → Failed: {result.get('error_message')}")
        return
    count = int(result.get("unlinked_count") or 0)
    print(f"  → Unlinked {count} linked service(s)")


# === 状态 ===

def trigger_payment(session_token: str) -> bool:
    """通过 orchestrator API 触发支付"""
    import urllib.request, urllib.error
    url = f"{ORCHESTRATOR_URL}/api/accounts/activate"
    data = json.dumps({"session_token": session_token}).encode()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"}, method="POST")
    try:
        resp = urllib.request.urlopen(req, timeout=30)
        result = json.loads(resp.read().decode())
        print(f"  → Payment triggered: {result}")
        return True
    except urllib.error.HTTPError as e:
        print(f"  → Payment failed: {e.code} {e.read().decode()[:200]}")
        return False


def account_setup(state, main_phone: str, pin: str, session_token: str = None, temp_phone: str = ""):
    """
    完整循环：改号 → 注销 → 登录 → 支付 → 解绑
    """
    print("=" * 60)
    print("GoPay Account Setup")
    print("=" * 60)

    if not temp_phone:
        print("FAILED: --new-phone is required for CLI account-setup; automatic number acquisition runs through orchestrator")
        return

    # Step 1: 改手机号到临时号
    print("\n[STEP 1] Change phone to temp number...")
    change_phone(state, temp_phone, pin)
    if not state.get("phone"):
        print("FAILED: Phone change did not complete")
        return

    # Step 2: 注销
    print("\n[STEP 2] Deactivate account on temp number...")
    deactivate(state, pin)
    if not state.get("deactivated_at"):
        print("FAILED: Deactivation did not complete")
        return

    # Step 3: 用主号重新登录
    print(f"\n[STEP 3] Login with main phone +62{main_phone}...")
    login(state, main_phone, pin)
    if not state.get("token"):
        print("FAILED: Login did not complete")
        return

    # Step 5: 触发支付
    if session_token:
        print("\n[STEP 4] Triggering payment...")
        trigger_payment(session_token)
    else:
        print("\n[STEP 4] SKIPPED: No --session-token provided. Trigger payment manually.")
        print(f"  Phone: +62{main_phone}, token is only present in the current state payload.")

    # Step 6: 解绑
    print("\n[STEP 5] Unlinking...")
    unlink(state)

    print("\n" + "=" * 60)
    print("Account setup complete! Ready for next round.")
    print("=" * 60)


def show_status(state, show_secrets: bool = False):
    print(json.dumps(state if show_secrets else _redacted_state(state), indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="GoPay 一号一Plus 循环")
    parser.add_argument("--step", required=True, choices=["change-phone", "deactivate", "login", "unlink", "status", "account-setup"])
    parser.add_argument("--new-phone", help="新手机号/临时手机号 (不含国家码)")
    parser.add_argument("--phone", help="主号码 (不含国家码)")
    parser.add_argument("--pin", default=GOPAY_PIN, help="GoPay PIN (默认读 GOPAY_PIN 环境变量)")
    parser.add_argument("--session-token", help="ChatGPT session token (account-setup用)")
    parser.add_argument("--show-secrets", action="store_true", help="status 时显示 token 等敏感值")
    args = parser.parse_args()

    state = load_state()

    if args.step == "change-phone":
        change_phone(state, args.new_phone or "", args.pin)
    elif args.step == "deactivate":
        deactivate(state, args.pin)
    elif args.step == "login":
        if not args.phone:
            print("需要 --phone")
            sys.exit(1)
        login(state, args.phone, args.pin)
    elif args.step == "unlink":
        unlink(state)
    elif args.step == "account-setup":
        if not args.phone:
            print("需要 --phone (主号码)")
            sys.exit(1)
        account_setup(state, args.phone, args.pin, args.session_token, args.new_phone or "")
    elif args.step == "status":
        show_status(state, args.show_secrets)
