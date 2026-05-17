"""
GoPay App gRPC Service - 显式步骤模式
外部编排服务按需调用改号、注销、登录等步骤；本服务不做后台自动任务。
"""

import contextvars
import json
import os
import re
import threading
import time
from concurrent import futures
from urllib.parse import parse_qs, quote, unquote, urlparse
from urllib.request import Request, urlopen

import grpc
import gopay_app_pb2
import gopay_app_pb2_grpc

from gopay_client import GopayClient
from gopay_app import (
    GOPAY_MIN_BALANCE_RP,
    GOPAY_PIN,
    access_token_expires_at,
    access_token_usable,
    complete_login,
    complete_signup,
    complete_signup_pin,
    ensure_access_token,
    ensure_state_device,
    check_token_valid,
    check_phone_by_login_methods,
    expire_login_if_needed,
    expire_signup_if_needed,
    get_qr_id,
    gopay_proxy_for_state,
    clear_tmp_tokens,
    migrate_active_tokens_to_tmp,
    start_login,
    start_signup,
    retry_signup_otp,
    retry_signup_pin_otp,
    start_signup_pin,
)
from replay import LinkPaymentOptions, LinkedAppUnlinkOptions, run_link_payment, run_linked_app_unlink

PORT = int(os.environ.get("GOPAY_APP_PORT", "50051"))
GOPAY_COUNTRY_CODE = os.environ.get("GOPAY_COUNTRY_CODE", "62").strip() or "62"
GOPAY_SIGNUP_NAME = os.environ.get("GOPAY_SIGNUP_NAME", "").strip()
GOPAY_SIGNUP_EMAIL = os.environ.get("GOPAY_SIGNUP_EMAIL", "").strip()
GOPAY_CHANGE_PHONE_CONFIRM_TIMEOUT_SECONDS = float(os.environ.get("GOPAY_CHANGE_PHONE_CONFIRM_TIMEOUT_SECONDS", "8"))
GOPAY_CHANGE_PHONE_CONFIRM_INTERVAL_SECONDS = float(os.environ.get("GOPAY_CHANGE_PHONE_CONFIRM_INTERVAL_SECONDS", "1"))
GOPAY_ENVELOPE_SHORTLINK_TIMEOUT_SECONDS = float(os.environ.get("GOPAY_ENVELOPE_SHORTLINK_TIMEOUT_SECONDS", "10"))
GOPAY_STATE_PG_DSN = (
    os.environ.get("GOPAY_APP_PG_DSN", "").strip()
    or os.environ.get("GOPAY_STATE_PG_DSN", "").strip()
    or os.environ.get("PG_DSN", "").strip()
)
GOPAY_STATE_TABLE = os.environ.get("GOPAY_STATE_TABLE", "gopay_app_states").strip() or "gopay_app_states"

GOPAY_API = "https://customer.gopayapi.com"
GOPAY_CUSTOMER = GOPAY_API
APP_GOPAY_HOST = "app.gopay.co.id"
GOJEK_API = "https://api.gojekapi.com"
GOTO_AUTH = "https://accounts.goto-products.com"
GOTO_SSO_CLIENT_ID = os.environ.get("GOTO_SSO_CLIENT_ID", "gojek:consumer:app")
GOTO_SSO_CLIENT_SECRET = os.environ.get("GOTO_SSO_CLIENT_SECRET", "")
GOPAY_CHANGE_PHONE_COUNTRY_SYNC = os.environ.get("GOPAY_CHANGE_PHONE_COUNTRY_SYNC", "").strip().lower() in {"1", "true", "yes", "on"}
_CURRENT_STATE = contextvars.ContextVar("gopay_app_state", default=None)
_STATE_STORE = None
_STATE_STORE_LOCK = threading.RLock()


def _parse_state_json(raw: str) -> dict:
    raw = str(raw or "").strip()
    if not raw:
        return {}
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ValueError("state_json must be a JSON object")
    return value


def _state_json(state: dict) -> str:
    return json.dumps(state or {}, ensure_ascii=False, separators=(",", ":"))


def _normalize_state_key(value: str) -> str:
    value = str(value or "").strip()
    if not value or value == "local":
        return "local"
    if value.startswith("tg:"):
        user_id = value.removeprefix("tg:").strip()
        if user_id and user_id.isdigit():
            return f"tg:{user_id}"
    raise ValueError("user_id must be local or tg:<user_id>")


class PostgresStateStore:
    def __init__(self, dsn: str, table: str):
        self.dsn = dsn
        self.table = table
        self._ready = False
        self._lock = threading.RLock()

    def _connect(self):
        try:
            import psycopg
        except ImportError as exc:
            raise RuntimeError("psycopg is required for gopay-app state persistence") from exc
        return psycopg.connect(self.dsn, autocommit=True)

    def _ensure_table(self) -> None:
        if self._ready:
            return
        with self._lock:
            if self._ready:
                return
            with self._connect() as conn:
                conn.execute(
                    f"""
                    CREATE TABLE IF NOT EXISTS {self.table} (
                        state_key TEXT PRIMARY KEY,
                        state_json JSONB NOT NULL DEFAULT '{{}}'::jsonb,
                        created_at BIGINT NOT NULL,
                        updated_at BIGINT NOT NULL
                    )
                    """
                )
            self._ready = True

    def load(self, state_key: str) -> str:
        self._ensure_table()
        with self._connect() as conn:
            row = conn.execute(
                f"SELECT state_json::text FROM {self.table} WHERE state_key = %s",
                (state_key,),
            ).fetchone()
        return str(row[0]) if row else "{}"

    def save(self, state_key: str, state_json: str) -> str:
        state = _parse_state_json(state_json)
        normalized = _state_json(state)
        self._ensure_table()
        now = int(time.time())
        with self._connect() as conn:
            conn.execute(
                f"""
                INSERT INTO {self.table} (state_key, state_json, created_at, updated_at)
                VALUES (%s, %s::jsonb, %s, %s)
                ON CONFLICT (state_key) DO UPDATE
                SET state_json = EXCLUDED.state_json,
                    updated_at = EXCLUDED.updated_at
                """,
                (state_key, normalized, now, now),
            )
        return normalized

    def delete(self, state_key: str) -> None:
        self._ensure_table()
        with self._connect() as conn:
            conn.execute(f"DELETE FROM {self.table} WHERE state_key = %s", (state_key,))


def _state_store() -> PostgresStateStore:
    global _STATE_STORE
    if not GOPAY_STATE_PG_DSN:
        raise RuntimeError("PG_DSN or GOPAY_STATE_PG_DSN is required for gopay-app state persistence")
    with _STATE_STORE_LOCK:
        if _STATE_STORE is None:
            _STATE_STORE = PostgresStateStore(GOPAY_STATE_PG_DSN, GOPAY_STATE_TABLE)
        return _STATE_STORE


def load_state():
    state = _CURRENT_STATE.get()
    return state if state is not None else {}


def save_state(state):
    # Stateless service: callers persist the returned state_json via DB CRUD.
    return None


def _state_from_request(request) -> dict:
    raw = getattr(request, "state_json", "")
    if raw:
        return _parse_state_json(raw)
    current = _CURRENT_STATE.get()
    return current if current is not None else {}


def _stateful_rpc(fn):
    def wrapped(self, request, context):
        state = _state_from_request(request)
        token = _CURRENT_STATE.set(state)
        try:
            resp = fn(self, request, context)
            if hasattr(resp, "state_json"):
                resp.state_json = _state_json(_CURRENT_STATE.get() or state)
            return resp
        finally:
            _CURRENT_STATE.reset(token)
    return wrapped


def _client(state) -> GopayClient:
    refresh = ensure_access_token(state)
    if not refresh.get("success") and not access_token_usable(state, 0):
        raise RuntimeError(refresh.get("error", "token refresh failed"))
    return GopayClient(state.get("token", ""), proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))


def _claim_envelope(client: GopayClient, envelope_request_id: str) -> dict:
    response = client.post(
        f"{GOPAY_CUSTOMER}/v1/festivals/envelope-requests",
        body={"envelope_request_id": envelope_request_id},
    )
    if int(response.get("status") or 0) == 200:
        detail = client.get(
            f"{GOPAY_CUSTOMER}/v1/festivals/envelope-requests/"
            f"{quote(envelope_request_id, safe='')}"
        )
        if int(detail.get("status") or 0) == 200:
            return {
                "status": 200,
                "data": detail.get("data"),
                "raw": {
                    "claim": response.get("raw") or response.get("data"),
                    "detail": detail.get("raw") or detail.get("data"),
                },
            }
    return response


def _phone_country_code(explicit: str = "") -> str:
    value = str(explicit or "").strip()
    if value:
        return value if value.startswith("+") else f"+{value}"
    return f"+{GOPAY_COUNTRY_CODE.lstrip('+')}"


def _normalize_phone(phone: str, country_code: str = "") -> str:
    prefix = _phone_country_code(country_code).lstrip("+")
    value = str(phone or "").strip().lstrip("+")
    if value.startswith(prefix):
        value = value[len(prefix):]
    return value


def _has_account_token(state) -> bool:
    return access_token_usable(state, 30)


def _tmp_access_token_usable(state: dict, min_ttl_seconds: int = 30) -> bool:
    token = str(state.get("_tmp_token", "")).strip()
    if not token:
        return False
    expires_at = access_token_expires_at(token) or int(state.get("_tmp_token_expires_at") or 0)
    if not expires_at:
        return True
    return expires_at > int(time.time()) + min_ttl_seconds


def _has_tmp_account_token(state: dict) -> bool:
    return _tmp_access_token_usable(state, 30)


def _tmp_client(state: dict) -> GopayClient:
    token = str(state.get("_tmp_token", "")).strip()
    if not token:
        raise RuntimeError("temporary account token missing")
    if not _tmp_access_token_usable(state, 0):
        expires_at = access_token_expires_at(token) or int(state.get("_tmp_token_expires_at") or 0)
        raise RuntimeError(f"temporary account token expired: expires_at={expires_at}")
    return GopayClient(token, proxy=gopay_proxy_for_state(state), device=ensure_state_device(state))


def _pin(request_pin: str = "") -> str:
    return str(request_pin or GOPAY_PIN or "").strip()


def _signup_seed(phone: str = "") -> str:
    digits = re.sub(r"\D", "", str(phone or ""))
    phone_tail = digits[-6:] if digits else ""
    return f"{phone_tail}{int(time.time())}{os.urandom(3).hex()}"


def _signup_name_from_seed(seed: str) -> str:
    alphabet = "abcdefghijklmnopqrstuvwxyz"
    hex_chars = re.sub(r"[^0-9a-f]", "", str(seed or "").lower())
    if len(hex_chars) < 2:
        hex_chars = f"{hex_chars:0>2}"
    return "".join(alphabet[int(ch, 16) % len(alphabet)] for ch in hex_chars[-2:])


def _signup_profile(phone: str = "", name: str = "", email: str = "") -> tuple[str, str]:
    resolved_name = str(name or GOPAY_SIGNUP_NAME or "").strip()
    resolved_email = str(email or GOPAY_SIGNUP_EMAIL or "").strip()
    if resolved_name:
        return resolved_name, resolved_email

    seed = _signup_seed(phone)
    resolved_name = _signup_name_from_seed(seed)
    return resolved_name, resolved_email


def _gojek_customer_profile(response: dict) -> dict:
    for key in ("data", "raw"):
        container = response.get(key) if isinstance(response, dict) else None
        if not isinstance(container, dict):
            continue
        customer = container.get("customer")
        if isinstance(customer, dict):
            return customer
    return {}


def _load_gojek_customer_profile(c: GopayClient) -> tuple[dict, str]:
    response = c.get(f"{GOJEK_API}/gojek/v2/customer")
    if response.get("status") != 200:
        return {}, _api_error("customer profile failed", response)
    profile = _gojek_customer_profile(response)
    if not profile:
        return {}, "customer profile missing"
    return profile, ""


def _sync_profile_fields_from_gojek(state: dict, profile: dict, country_code: str) -> None:
    name = str(profile.get("name") or "").strip()
    email = str(profile.get("email") or "").strip()
    phone = str(profile.get("phone") or profile.get("number") or "").strip()
    if name:
        state["name"] = name
    if email:
        state["email"] = email
    if phone:
        state["phone"] = _normalize_phone(phone, country_code)


def _fallback_profile_email(state: dict, country_code: str, normalized_phone: str) -> str:
    for key in ("email", "_signup_email"):
        email = str(state.get(key) or "").strip()
        if email:
            return email
    email = GOPAY_SIGNUP_EMAIL
    if email:
        return email
    digits = re.sub(r"\D", "", f"{country_code}{normalized_phone}")
    if not digits:
        digits = str(int(time.time()))
    return f"gopay{digits}@gmail.com"


def _change_phone_profile_body(state: dict, country_code: str, normalized_phone: str) -> dict:
    changed = False
    name = str(state.get("name") or "").strip()
    if not name:
        name = "gg"
        state["name"] = name
        changed = True
    email = str(state.get("email") or "").strip()
    if not email:
        email = _fallback_profile_email(state, country_code, normalized_phone)
        state["email"] = email
        changed = True
    if changed:
        save_state(state)
    return {
        "email": email,
        "name": name,
        "phone": f"{country_code}{normalized_phone}",
        "profile_image_url": None,
    }


def _api_error(label: str, response: dict) -> str:
    status = response.get("status")
    if status == 401:
        return "AUTH_INVALID"
    detail = response.get("raw") or response.get("data")
    return f"{label}: status {status} {detail}"


def _response_errors(response: dict) -> list:
    raw = response.get("raw") if isinstance(response.get("raw"), dict) else {}
    data = response.get("data") if isinstance(response.get("data"), dict) else {}
    errors = raw.get("errors") or data.get("errors") or []
    return errors if isinstance(errors, list) else []


def _response_text(response: dict) -> str:
    return str(response.get("raw") or response.get("data") or "")


def _decoded_candidates(value: str) -> list[str]:
    current = str(value or "").strip()
    candidates = []
    seen = set()
    for _ in range(5):
        if current and current not in seen:
            candidates.append(current)
            seen.add(current)
        decoded = unquote(current)
        if decoded == current:
            break
        current = decoded
    return candidates


def _extract_envelope_request_id(value: str) -> str:
    for candidate in _decoded_candidates(value):
        path_match = re.search(r"/v1/festivals/envelope-requests/([^/?#'\" <]+)", candidate)
        if path_match:
            return path_match.group(1)

        parsed = urlparse(candidate)
        query = parse_qs(parsed.query)
        for key in ("envelope_request_id", "envelopeRequestId", "id"):
            for item in query.get(key, []):
                nested = _extract_envelope_request_id(item)
                if nested:
                    return nested

        for key in ("data", "link", "deep_link_value", "af_dp"):
            for item in query.get(key, []):
                nested = _extract_envelope_request_id(item)
                if nested:
                    return nested

        field_match = re.search(r"envelope_request_id['\"\s:=]+([A-Za-z0-9_-]{8,128})", candidate)
        if field_match:
            return field_match.group(1)

        if re.fullmatch(r"[A-Za-z0-9_-]{8,128}", candidate):
            return candidate
    return ""


def _is_app_gopay_shortlink(value: str) -> bool:
    raw = str(value or "").strip()
    if not raw:
        return False
    parsed = urlparse(raw if "://" in raw else f"https://{raw}")
    return parsed.netloc.lower() == APP_GOPAY_HOST


def _fetch_shortlink_html(link: str) -> str:
    url = str(link or "").strip()
    if "://" not in url:
        url = f"https://{url}"
    req = Request(
        url,
        headers={
            "User-Agent": (
                "Mozilla/5.0 (Linux; Android 11; sdk_gphone_arm64) "
                "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Mobile Safari/537.36"
            ),
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        },
    )
    with urlopen(req, timeout=max(1.0, GOPAY_ENVELOPE_SHORTLINK_TIMEOUT_SECONDS)) as resp:
        charset = resp.headers.get_content_charset() or "utf-8"
        return resp.read(1024 * 1024).decode(charset, errors="replace")


def _resolve_envelope_request_id(envelope_request_id: str, link: str) -> str:
    for source in (envelope_request_id, link):
        resolved = _extract_envelope_request_id(source)
        if resolved:
            return resolved
    if _is_app_gopay_shortlink(link):
        resolved = _extract_envelope_request_id(_fetch_shortlink_html(link))
        if resolved:
            return resolved
    raise ValueError("envelope_request_id required, or pass a link containing one")


def _json_compact(value) -> str:
    return json.dumps(value if value is not None else {}, ensure_ascii=False, separators=(",", ":"), default=str)


def _confirm_change_phone(c: GopayClient, country_code: str, expected_phone: str) -> tuple[bool, str]:
    expected = _normalize_phone(expected_phone, country_code)
    deadline = time.monotonic() + max(0.0, GOPAY_CHANGE_PHONE_CONFIRM_TIMEOUT_SECONDS)
    last_error = ""

    while True:
        profile, err = _load_gojek_customer_profile(c)
        if err:
            last_error = err
        else:
            actual = _normalize_phone(profile.get("phone") or profile.get("number") or "", country_code)
            if actual == expected:
                return True, ""
            last_error = f"phone change not confirmed: expected {expected}, got {actual or '-'}"

        if time.monotonic() >= deadline:
            return False, last_error
        time.sleep(max(0.1, GOPAY_CHANGE_PHONE_CONFIRM_INTERVAL_SECONDS))


def _deactivation_otp_required(response: dict) -> bool:
    if response.get("status") == 462:
        return True
    for err in _response_errors(response):
        if isinstance(err, dict) and str(err.get("code", "")).upper() == "GOPAY-1603":
            return True
    return False


def _deactivation_check_ready(response: dict) -> bool:
    return response.get("status") == 200 or _deactivation_otp_required(response)


def _phone_registered_response(response: dict) -> bool:
    if response.get("status") != 400:
        return False
    text = _response_text(response).lower()
    return "user_can_not_update_phone" in text or "already registered" in text


def _token_check_ready(result: dict) -> bool:
    return bool(result.get("success") and result.get("token_valid") and result.get("has_min_balance"))


def _token_check_valid(result: dict) -> bool:
    return bool(result.get("token_valid"))


def _token_check_error(result: dict) -> str:
    if result.get("error"):
        return str(result.get("error"))
    amount = int(result.get("balance_amount") or 0)
    currency = str(result.get("balance_currency") or "IDR")
    return f"insufficient gopay balance: {amount} {currency} < {GOPAY_MIN_BALANCE_RP} IDR"


def _complete_login(otp: str):
    """完成 GoPay 登录（收到主号 OTP 后调用）"""
    state = load_state()
    complete_login(state, otp)
    print(f"  Logged in! Token ready.")


class GopayAppServicer(gopay_app_pb2_grpc.GopayAppServiceServicer):

    def GetGoPayState(self, request, context):
        try:
            state_key = _normalize_state_key(request.user_id)
            state_json = _state_store().load(state_key)
            return gopay_app_pb2.GetGoPayStateResponse(
                success=True,
                user_id=state_key,
                state_json=state_json or "{}",
            )
        except Exception as e:
            return gopay_app_pb2.GetGoPayStateResponse(success=False, error_message=str(e))

    def UpsertGoPayState(self, request, context):
        try:
            state_key = _normalize_state_key(request.user_id)
            state_json = _state_store().save(state_key, request.state_json or "{}")
            return gopay_app_pb2.UpsertGoPayStateResponse(
                success=True,
                user_id=state_key,
                state_json=state_json,
            )
        except Exception as e:
            return gopay_app_pb2.UpsertGoPayStateResponse(success=False, error_message=str(e))

    def DeleteGoPayState(self, request, context):
        try:
            state_key = _normalize_state_key(request.user_id)
            _state_store().delete(state_key)
            return gopay_app_pb2.DeleteGoPayStateResponse(success=True)
        except Exception as e:
            return gopay_app_pb2.DeleteGoPayStateResponse(success=False, error_message=str(e))

    def CheckPhone(self, request, context):
        try:
            country_code = _phone_country_code(request.country_code)
            prefix = country_code.lstrip("+")
            normalized_phone = str(request.phone or "").strip().lstrip("+")
            if normalized_phone.startswith(prefix):
                normalized_phone = normalized_phone[len(prefix):]
            if not normalized_phone:
                return gopay_app_pb2.CheckPhoneResponse(
                    available=False, status="error", error_message="phone required")

            result = check_phone_by_login_methods(normalized_phone, country_code)
            if result.get("success") and result.get("available"):
                return gopay_app_pb2.CheckPhoneResponse(available=True, status="available")
            if result.get("success") and result.get("status") == "registered":
                return gopay_app_pb2.CheckPhoneResponse(
                    available=False, status="registered", error_message="PHONE_REGISTERED")
            if result.get("status") == "rate_limited":
                return gopay_app_pb2.CheckPhoneResponse(
                    available=False, status="rate_limited", error_message=str(result.get("error") or "RATE_LIMITED"))
            return gopay_app_pb2.CheckPhoneResponse(
                available=False, status="error", error_message=str(result.get("error") or "check phone failed"))
        except Exception as e:
            return gopay_app_pb2.CheckPhoneResponse(
                available=False, status="error", error_message=str(e))

    def ReplayLinkPayment(self, request, context):
        try:
            state = load_state()
            client = _client(state)
            options = LinkPaymentOptions(
                payment_link=str(request.payment_link or "").strip(),
                pin=str(request.pin or GOPAY_PIN or "").strip(),
                amount_value=int(request.amount_value or 1),
                amount_currency=str(request.amount_currency or "IDR").strip() or "IDR",
                body_limit=int(request.body_limit or 1200),
            )
            result = run_link_payment(client, options)
            return gopay_app_pb2.ReplayLinkPaymentResponse(
                success=bool(result.get("success")),
                error_message=str(result.get("error_message") or ""),
                payment_id=str(result.get("payment_id") or ""),
                status=str(result.get("status") or ""),
                steps=[
                    gopay_app_pb2.ReplayStepResult(
                        label=str(item.get("label") or ""),
                        status_code=int(item.get("status_code") or 0),
                        response_text=str(item.get("response_text") or ""),
                        error_message=str(item.get("error_message") or ""),
                    )
                    for item in result.get("steps", [])
                ],
            )
        except Exception as e:
            return gopay_app_pb2.ReplayLinkPaymentResponse(success=False, error_message=str(e))

    def ClaimEnvelope(self, request, context):
        try:
            state = load_state()
            envelope_request_id = _resolve_envelope_request_id(
                request.envelope_request_id,
                request.link,
            )
            response = _claim_envelope(_client(state), envelope_request_id)
            raw = response.get("raw") or response.get("data") or {}
            data = response.get("data") if isinstance(response.get("data"), dict) else {}
            success = int(response.get("status") or 0) == 200
            return gopay_app_pb2.ClaimEnvelopeResponse(
                success=success,
                error_message="" if success else _api_error("claim envelope failed", response),
                envelope_request_id=envelope_request_id,
                response_envelope_request_id=str(data.get("envelope_request_id") or ""),
                status=str(data.get("status") or ""),
                http_status=int(response.get("status") or 0),
                raw_json=_json_compact(raw),
            )
        except Exception as e:
            return gopay_app_pb2.ClaimEnvelopeResponse(success=False, error_message=str(e))

    def ChangePhoneStart(self, request, context):
        try:
            state = load_state()
            pin = _pin(request.pin)
            phone = str(request.new_phone or "").strip()
            country_code = _phone_country_code()
            prefix = country_code.lstrip("+")
            normalized_phone = phone.lstrip("+")
            if normalized_phone.startswith(prefix):
                normalized_phone = normalized_phone[len(prefix):]

            if not _has_account_token(state):
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="account token missing")
            if not pin:
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="gopay pin missing")
            if not normalized_phone:
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="new_phone required")

            usage_key = f"_temp_phone_usage_{normalized_phone}"
            if state.get(usage_key, 0) >= 2:
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="PHONE_EXHAUSTED")

            ensure_state_device(state)
            c = _client(state)
            profile, profile_error = _load_gojek_customer_profile(c)
            if profile:
                _sync_profile_fields_from_gojek(state, profile, country_code)
            elif not str(state.get("email") or "").strip():
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message=profile_error)
            body = _change_phone_profile_body(state, country_code, normalized_phone)

            checked = (
                state.get("_checked_change_phone") == normalized_phone
                and state.get("_checked_change_phone_status") == "available"
            )
            if not checked:
                r = c.patch(f"{GOJEK_API}/v5/customers", body=body)
                if _phone_registered_response(r):
                    return gopay_app_pb2.ChangePhoneStartResponse(
                        success=False, error_message="PHONE_REGISTERED")
                if r["status"] != 461:
                    return gopay_app_pb2.ChangePhoneStartResponse(
                        success=False, error_message=_api_error("pin challenge failed", r))

            r = c.patch(f"{GOJEK_API}/v5/customers", body=body, extra_headers={"pin": pin})
            if _phone_registered_response(r):
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="PHONE_REGISTERED")
            if r["status"] != 200:
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message=_api_error("pin submit failed", r))

            data = r.get("data") or {}
            otp_token = data.get("otp_token", "")
            if not otp_token:
                return gopay_app_pb2.ChangePhoneStartResponse(
                    success=False, error_message="otp_token missing")

            state["_change_phone"] = normalized_phone
            state["_change_otp_token"] = otp_token
            state.pop("_checked_change_phone", None)
            state.pop("_checked_change_phone_status", None)
            now = int(time.time())
            state["_change_otp_sent_at"] = now
            state["_change_otp_expires_at"] = now + int(data.get("expires_in") or 300)
            state["stage"] = "change_phone_otp_pending"
            state.pop("last_error", None)
            save_state(state)
            return gopay_app_pb2.ChangePhoneStartResponse(
                success=True, new_phone=normalized_phone, otp_sent=True)
        except Exception as e:
            return gopay_app_pb2.ChangePhoneStartResponse(success=False, error_message=str(e))

    def ChangePhoneRetry(self, request, context):
        try:
            state = load_state()
            otp_token = state.get("_change_otp_token", "")
            phone = state.get("_change_phone", "")
            if not otp_token or not phone:
                return gopay_app_pb2.ChangePhoneRetryResponse(
                    success=False, error_message=f"not waiting for change phone otp: {state.get('stage', 'idle')}")

            c = _client(state)
            r = c.post(f"{GOJEK_API}/v2/otp/retry", body={
                "otp_token": otp_token,
                "channel_type": "sms",
            })
            if r["status"] != 200:
                return gopay_app_pb2.ChangePhoneRetryResponse(
                    success=False, error_message=_api_error("otp retry failed", r))

            data = r.get("data") or {}
            new_otp_token = data.get("otp_token", "")
            if not new_otp_token:
                return gopay_app_pb2.ChangePhoneRetryResponse(
                    success=False, error_message="retry otp_token missing")

            now = int(time.time())
            state["_change_otp_token"] = new_otp_token
            state["_change_otp_sent_at"] = now
            state["_change_otp_expires_at"] = now + int(data.get("otp_expires_in") or 300)
            state["stage"] = "change_phone_otp_pending"
            state.pop("last_error", None)
            save_state(state)
            return gopay_app_pb2.ChangePhoneRetryResponse(success=True, otp_sent=True)
        except Exception as e:
            return gopay_app_pb2.ChangePhoneRetryResponse(success=False, error_message=str(e))

    def ChangePhoneComplete(self, request, context):
        try:
            state = load_state()
            otp_token = state.get("_change_otp_token", "")
            phone = state.get("_change_phone", "")
            if not otp_token or not phone:
                return gopay_app_pb2.ChangePhoneCompleteResponse(
                    success=False, error_message=f"not waiting for change phone otp: {state.get('stage', 'idle')}")
            if not request.otp:
                return gopay_app_pb2.ChangePhoneCompleteResponse(
                    success=False, error_message="otp required")

            c = _client(state)
            r = c.post(f"{GOJEK_API}/v5/customers/verificationUpdateProfile",
                       body={"otp": request.otp, "otp_token": otp_token})
            if r["status"] != 200:
                return gopay_app_pb2.ChangePhoneCompleteResponse(
                    success=False, error_message=_api_error("otp verify failed", r))

            confirmed, confirm_error = _confirm_change_phone(c, _phone_country_code(), phone)
            if not confirmed:
                state["last_error"] = confirm_error
                save_state(state)
                return gopay_app_pb2.ChangePhoneCompleteResponse(
                    success=False, error_message=confirm_error)

            if GOPAY_CHANGE_PHONE_COUNTRY_SYNC:
                r = c.put(f"{GOPAY_API}/customers/v1/country-change")
                if r["status"] != 200:
                    return gopay_app_pb2.ChangePhoneCompleteResponse(
                        success=False, error_message=_api_error("country-change sync failed", r))

            state["phone"] = phone
            usage_key = f"_temp_phone_usage_{phone}"
            state[usage_key] = state.get(usage_key, 0) + 1
            migrate_active_tokens_to_tmp(state, phone=phone)
            state["stage"] = "phone_changed"
            state.pop("last_error", None)
            state.pop("_change_phone", None)
            state.pop("_change_otp_token", None)
            state.pop("_change_otp_sent_at", None)
            state.pop("_change_otp_expires_at", None)
            save_state(state)
            return gopay_app_pb2.ChangePhoneCompleteResponse(success=True)
        except Exception as e:
            return gopay_app_pb2.ChangePhoneCompleteResponse(success=False, error_message=str(e))

    def DeactivateStart(self, request, context):
        try:
            state = load_state()
            pin = _pin(request.pin)
            if not _has_tmp_account_token(state):
                return gopay_app_pb2.DeactivateStartResponse(
                    success=False, error_message="temporary account token missing")

            c = _tmp_client(state)
            profile = c.get(f"{GOPAY_CUSTOMER}/v1/users/profile")
            pin_setup = False
            if profile["status"] == 200:
                pin_setup = bool((profile.get("data") or {}).get("is_pin_setup"))
            elif pin:
                pin_setup = True

            if pin_setup:
                if not pin:
                    return gopay_app_pb2.DeactivateStartResponse(
                        success=False, error_message="gopay pin missing")
                r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pin/challenges", body={"flow": "deactivation"})
                if r["status"] != 200:
                    return gopay_app_pb2.DeactivateStartResponse(
                        success=False, error_message=_api_error("deactivation challenge failed", r))

                challenge_id = r["data"].get("challenge_id", "")
                client_id = r["data"].get("client_id", "")
                if not challenge_id or not client_id:
                    return gopay_app_pb2.DeactivateStartResponse(
                        success=False, error_message="deactivation challenge missing id")

                r = c.post(f"{GOPAY_CUSTOMER}/api/v1/users/pin/tokens",
                           body={"challenge_id": challenge_id, "client_id": client_id, "pin": pin})
                if r["status"] != 200:
                    return gopay_app_pb2.DeactivateStartResponse(
                        success=False, error_message=_api_error("deactivation pin verify failed", r))

            r = c.get(f"{GOPAY_API}/api/v1/users/deactivate/check")
            if not _deactivation_check_ready(r):
                return gopay_app_pb2.DeactivateStartResponse(
                    success=False, error_message=_api_error("deactivation check failed", r))

            state["stage"] = "deactivate_otp_pending"
            state.pop("last_error", None)
            save_state(state)
            return gopay_app_pb2.DeactivateStartResponse(success=True, otp_sent=True)
        except Exception as e:
            return gopay_app_pb2.DeactivateStartResponse(success=False, error_message=str(e))

    def DeactivateComplete(self, request, context):
        try:
            state = load_state()
            if not request.otp:
                return gopay_app_pb2.DeactivateCompleteResponse(
                    success=False, error_message="otp required")

            c = _tmp_client(state)
            r = c.delete(f"{GOPAY_CUSTOMER}/api/v1/users/deactivate",
                         body={
                             "otp": request.otp,
                             "reason": "I no longer need digital payment services",
                             "description": None,
                         })
            if r["status"] != 200:
                return gopay_app_pb2.DeactivateCompleteResponse(
                    success=False, error_message=_api_error("deactivate failed", r))

            deactivated_at = int(time.time())
            state["deactivated_at"] = deactivated_at
            state["stage"] = "deactivated"
            state.pop("last_error", None)
            clear_tmp_tokens(state)
            save_state(state)
            return gopay_app_pb2.DeactivateCompleteResponse(success=True, deactivated_at=deactivated_at)
        except Exception as e:
            return gopay_app_pb2.DeactivateCompleteResponse(success=False, error_message=str(e))

    def LoginStart(self, request, context):
        try:
            state = load_state()
            if expire_login_if_needed(state):
                save_state(state)
            stage = str(state.get("stage", "")).strip()
            if stage in ("ready", "consumed"):
                token_check = check_token_valid(state)
                state = load_state()
                if _token_check_ready(token_check):
                    return gopay_app_pb2.LoginStartResponse(success=True, otp_sent=False)
                if token_check.get("token_valid"):
                    return gopay_app_pb2.LoginStartResponse(
                        success=False,
                        error_message=_token_check_error(token_check),
                    )
                print(f"[gopay-app] ready token validation failed; falling back to GoPay login: {token_check.get('error', '')}")

            phone = str(request.phone or "").strip()
            if not phone and stage in ("login", "login_otp_pending"):
                phone = str(state.get("_login_phone", "")).strip()
            if not phone:
                return gopay_app_pb2.LoginStartResponse(
                    success=False, error_message="login phone required")
            country_code = _phone_country_code(request.country_code)
            prefix = country_code.lstrip("+")
            normalized_phone = phone.lstrip("+")
            if normalized_phone.startswith(prefix):
                normalized_phone = normalized_phone[len(prefix):]

            if stage == "login_otp_pending" and state.get("_login_otp_token") and state.get("_login_2fa_token"):
                return gopay_app_pb2.LoginStartResponse(
                    success=True,
                    otp_sent=True,
                    verification_id=state.get("_login_verification_id", ""),
                    verification_method=state.get("_login_verification_method", ""),
                )

            pin = str(request.pin or "").strip()
            if not pin:
                return gopay_app_pb2.LoginStartResponse(
                    success=False, error_message="login pin required")
            result = start_login(state, normalized_phone, pin, country_code, request.otp_channel)
            if not result.get("success"):
                if result.get("not_registered"):
                    return gopay_app_pb2.LoginStartResponse(
                        success=False, error_message="账户未注册")
                return gopay_app_pb2.LoginStartResponse(
                    success=False, error_message=result.get("error", "login start failed"))
            if result.get("ready"):
                state = load_state()
                token_check = check_token_valid(state)
                state = load_state()
                if not _token_check_ready(token_check):
                    return gopay_app_pb2.LoginStartResponse(
                        success=False,
                        error_message=_token_check_error(token_check),
                    )
            return gopay_app_pb2.LoginStartResponse(
                success=True,
                otp_sent=bool(result.get("otp_sent")),
                verification_id=result.get("verification_id", ""),
                verification_method=result.get("method", ""),
            )
        except Exception as e:
            return gopay_app_pb2.LoginStartResponse(success=False, error_message=str(e))

    def LoginComplete(self, request, context):
        """提交主号 OTP 完成 GoPay 登录"""
        try:
            state = load_state()
            if expire_login_if_needed(state):
                save_state(state)
            if state.get("stage") != "login_otp_pending":
                return gopay_app_pb2.LoginCompleteResponse(
                    success=False, error_message=f"not waiting for login otp: {state.get('stage', 'idle')}")
            _complete_login(request.otp)
            state = load_state()
            return gopay_app_pb2.LoginCompleteResponse(
                success=True, phone=state.get("phone", ""))
        except Exception as e:
            return gopay_app_pb2.LoginCompleteResponse(success=False, error_message=str(e))

    def SignupStart(self, request, context):
        try:
            state = load_state()
            changed = expire_login_if_needed(state) or expire_signup_if_needed(state)
            if changed:
                save_state(state)
            phone = str(request.phone or "").strip()
            if not phone:
                return gopay_app_pb2.SignupStartResponse(
                    success=False,
                    error_message="signup phone required",
                )
            name, email = _signup_profile(phone, request.name, request.email)
            country_code = _phone_country_code(request.country_code)
            result = start_signup(state, phone, name, email, country_code, request.otp_channel)
            if not result.get("success"):
                return gopay_app_pb2.SignupStartResponse(
                    success=False,
                    error_message=result.get("error", "signup start failed"),
                    raw_json=result.get("raw_json", ""),
                )
            return gopay_app_pb2.SignupStartResponse(
                success=True,
                otp_sent=bool(result.get("otp_sent")),
                verification_id=result.get("verification_id", ""),
                verification_method=result.get("method", ""),
                raw_json=result.get("raw_json", ""),
                retry_timer_seconds=[int(value) for value in result.get("retry_timer_seconds", []) if str(value).strip().isdigit()],
            )
        except Exception as e:
            return gopay_app_pb2.SignupStartResponse(success=False, error_message=str(e))

    def SignupRetry(self, request, context):
        try:
            state = load_state()
            result = retry_signup_otp(state)
            if not result.get("success"):
                return gopay_app_pb2.SignupRetryResponse(
                    success=False,
                    error_message=result.get("error", "signup retry failed"),
                    raw_json=result.get("raw_json", ""),
                )
            return gopay_app_pb2.SignupRetryResponse(
                success=True,
                otp_sent=bool(result.get("otp_sent")),
                raw_json=result.get("raw_json", ""),
            )
        except Exception as e:
            return gopay_app_pb2.SignupRetryResponse(success=False, error_message=str(e))

    def SignupComplete(self, request, context):
        try:
            state = load_state()
            if expire_signup_if_needed(state):
                save_state(state)
            result = complete_signup(state, request.otp)
            if not result.get("success"):
                return gopay_app_pb2.SignupCompleteResponse(
                    success=False,
                    error_message=result.get("error", "signup complete failed"),
                    raw_json=result.get("raw_json", ""),
                )
            return gopay_app_pb2.SignupCompleteResponse(
                success=True,
                phone=result.get("phone", ""),
                pin_setup_required=bool(result.get("pin_setup_required")),
                raw_json=result.get("raw_json", ""),
            )
        except Exception as e:
            return gopay_app_pb2.SignupCompleteResponse(success=False, error_message=str(e))

    def CreatePinStart(self, request, context):
        try:
            state = load_state()
            if expire_signup_if_needed(state):
                save_state(state)
            result = start_signup_pin(state, _pin(request.pin), request.otp_channel)
            if not result.get("success"):
                return gopay_app_pb2.CreatePinStartResponse(
                    success=False,
                    error_message=result.get("error", "create pin start failed"),
                )
            return gopay_app_pb2.CreatePinStartResponse(
                success=True,
                otp_sent=bool(result.get("otp_sent")),
                verification_id=result.get("verification_id", ""),
                verification_method=result.get("method", ""),
            )
        except Exception as e:
            return gopay_app_pb2.CreatePinStartResponse(success=False, error_message=str(e))

    def CreatePinRetry(self, request, context):
        try:
            state = load_state()
            if expire_signup_if_needed(state):
                save_state(state)
            result = retry_signup_pin_otp(state)
            if not result.get("success"):
                return gopay_app_pb2.CreatePinRetryResponse(
                    success=False,
                    error_message=result.get("error", "create pin retry failed"),
                )
            return gopay_app_pb2.CreatePinRetryResponse(
                success=True,
                otp_sent=bool(result.get("otp_sent")),
            )
        except Exception as e:
            return gopay_app_pb2.CreatePinRetryResponse(success=False, error_message=str(e))

    def CreatePinComplete(self, request, context):
        try:
            state = load_state()
            if expire_signup_if_needed(state):
                save_state(state)
            result = complete_signup_pin(state, request.otp, _pin(request.pin))
            if not result.get("success"):
                return gopay_app_pb2.CreatePinCompleteResponse(
                    success=False,
                    error_message=result.get("error", "create pin complete failed"),
                )
            return gopay_app_pb2.CreatePinCompleteResponse(
                success=True,
                phone=result.get("phone", ""),
                pin_setup_complete=bool(result.get("pin_setup_complete")),
            )
        except Exception as e:
            return gopay_app_pb2.CreatePinCompleteResponse(success=False, error_message=str(e))

    def AuthStart(self, request, context):
        try:
            state = load_state()
            changed = expire_login_if_needed(state) or expire_signup_if_needed(state)
            if changed:
                save_state(state)
                state = load_state()

            stage = str(state.get("stage", "")).strip()
            if stage in ("signup_pin_required", "signup_pin_otp_pending"):
                token_check = check_token_valid(state)
                state = load_state()
                state["stage"] = stage
                save_state(state)
                if not _token_check_valid(token_check):
                    return gopay_app_pb2.AuthStartResponse(
                        success=False,
                        error_message=_token_check_error(token_check),
                        mode="signup",
                        stage=stage,
                        pin_setup_required=True,
                    )
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="signup",
                    stage=stage,
                    otp_sent=stage == "signup_pin_otp_pending",
                    verification_id=state.get("_signup_pin_verification_id", ""),
                    verification_method=state.get("_signup_pin_verification_method", ""),
                    pin_setup_required=True,
                )

            token_check = check_token_valid(state)
            state = load_state()
            if _token_check_valid(token_check):
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="token",
                    stage=state.get("stage", "ready"),
                    ready=True,
                )
            if stage == "login_otp_pending":
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="login",
                    stage=stage,
                    otp_sent=True,
                    verification_id=state.get("_login_verification_id", ""),
                    verification_method=state.get("_login_verification_method", ""),
                )
            if stage == "signup_pin_otp_pending":
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="signup",
                    stage=stage,
                    otp_sent=True,
                    verification_id=state.get("_signup_pin_verification_id", ""),
                    verification_method=state.get("_signup_pin_verification_method", ""),
                    pin_setup_required=True,
                )
            if stage == "signup_pin_required":
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="signup",
                    stage=stage,
                    pin_setup_required=True,
                )
            if stage == "signup_otp_pending":
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="signup",
                    stage=stage,
                    otp_sent=True,
                    verification_id=state.get("_signup_verification_id", ""),
                    verification_method=state.get("_signup_verification_method", ""),
                )

            phone = str(request.phone or "").strip()
            if not phone:
                return gopay_app_pb2.AuthStartResponse(success=False, error_message="auth phone required")
            country_code = _phone_country_code(request.country_code)
            prefix = country_code.lstrip("+")
            normalized_phone = phone.lstrip("+")
            if normalized_phone.startswith(prefix):
                normalized_phone = normalized_phone[len(prefix):]

            result = start_login(state, normalized_phone, _pin(request.pin), country_code, request.otp_channel)
            if result.get("success"):
                state = load_state()
                ready = bool(result.get("ready"))
                if ready:
                    token_check = check_token_valid(state)
                    state = load_state()
                    if not _token_check_valid(token_check):
                        return gopay_app_pb2.AuthStartResponse(
                            success=False,
                            error_message=_token_check_error(token_check),
                            mode="login",
                            stage=state.get("stage", "idle"),
                            ready=False,
                        )
                return gopay_app_pb2.AuthStartResponse(
                    success=True,
                    mode="login",
                    stage=state.get("stage", "idle"),
                    otp_sent=bool(result.get("otp_sent")),
                    verification_id=result.get("verification_id", ""),
                    verification_method=result.get("method", ""),
                    ready=ready,
                )
            if not result.get("not_registered"):
                state = load_state()
                return gopay_app_pb2.AuthStartResponse(
                    success=False,
                    error_message=result.get("error", "login start failed"),
                    mode="login",
                    stage=state.get("stage", "idle"),
                )

            signup_name, signup_email = _signup_profile(normalized_phone)
            resp = self.SignupStart(
                gopay_app_pb2.SignupStartRequest(
                    phone=normalized_phone,
                    name=signup_name,
                    email=signup_email,
                    country_code=country_code,
                    otp_channel=request.otp_channel,
                ),
                context,
            )
            state = load_state()
            return gopay_app_pb2.AuthStartResponse(
                success=resp.success,
                error_message=resp.error_message,
                mode="signup",
                stage=state.get("stage", "idle"),
                otp_sent=resp.otp_sent,
                verification_id=resp.verification_id,
                verification_method=resp.verification_method,
            )
        except Exception as e:
            return gopay_app_pb2.AuthStartResponse(success=False, error_message=str(e))

    def AuthComplete(self, request, context):
        try:
            state = load_state()
            changed = expire_login_if_needed(state) or expire_signup_if_needed(state)
            if changed:
                save_state(state)
                state = load_state()
            stage = str(state.get("stage", "")).strip()

            if stage == "ready" and _has_account_token(state):
                token_check = check_token_valid(state)
                state = load_state()
                if _token_check_valid(token_check):
                    return gopay_app_pb2.AuthCompleteResponse(
                        success=True,
                        mode="token",
                        stage="ready",
                        phone=state.get("phone", ""),
                        ready=True,
                    )
                return gopay_app_pb2.AuthCompleteResponse(
                    success=False,
                    error_message=_token_check_error(token_check),
                    mode="token",
                    stage=state.get("stage", "ready"),
                    phone=state.get("phone", ""),
                )
            if stage == "login_otp_pending":
                resp = self.LoginComplete(
                    gopay_app_pb2.LoginCompleteRequest(otp=request.otp),
                    context,
                )
                state = load_state()
                ready = resp.success and state.get("stage") == "ready"
                if ready:
                    token_check = check_token_valid(state)
                    state = load_state()
                    if not _token_check_valid(token_check):
                        return gopay_app_pb2.AuthCompleteResponse(
                            success=False,
                            error_message=_token_check_error(token_check),
                            mode="login",
                            stage=state.get("stage", "idle"),
                            phone=resp.phone,
                        )
                return gopay_app_pb2.AuthCompleteResponse(
                    success=resp.success,
                    error_message=resp.error_message,
                    mode="login",
                    stage=state.get("stage", "idle"),
                    phone=resp.phone,
                    ready=ready,
                )

            if stage == "signup_otp_pending":
                resp = self.SignupComplete(
                    gopay_app_pb2.SignupCompleteRequest(otp=request.otp),
                    context,
                )
                state = load_state()
                ready = resp.success and state.get("stage") == "ready"
                if ready:
                    token_check = check_token_valid(state)
                    state = load_state()
                    if not _token_check_valid(token_check):
                        return gopay_app_pb2.AuthCompleteResponse(
                            success=False,
                            error_message=_token_check_error(token_check),
                            mode="signup",
                            stage=state.get("stage", "idle"),
                            phone=resp.phone,
                            pin_setup_required=resp.pin_setup_required,
                        )
                return gopay_app_pb2.AuthCompleteResponse(
                    success=resp.success,
                    error_message=resp.error_message,
                    mode="signup",
                    stage=state.get("stage", "idle"),
                    phone=resp.phone,
                    pin_setup_required=resp.pin_setup_required,
                    ready=ready,
                )
            if stage == "signup_pin_otp_pending":
                resp = self.CreatePinComplete(
                    gopay_app_pb2.CreatePinCompleteRequest(otp=request.otp, pin=request.pin),
                    context,
                )
                state = load_state()
                ready = resp.success and state.get("stage") == "ready"
                if ready:
                    token_check = check_token_valid(state)
                    state = load_state()
                    if not _token_check_valid(token_check):
                        return gopay_app_pb2.AuthCompleteResponse(
                            success=False,
                            error_message=_token_check_error(token_check),
                            mode="signup",
                            stage=state.get("stage", "idle"),
                            phone=resp.phone,
                            pin_setup_complete=resp.pin_setup_complete,
                        )
                return gopay_app_pb2.AuthCompleteResponse(
                    success=resp.success,
                    error_message=resp.error_message,
                    mode="signup",
                    stage=state.get("stage", "idle"),
                    phone=resp.phone,
                    pin_setup_complete=resp.pin_setup_complete,
                    ready=ready,
                )
            return gopay_app_pb2.AuthCompleteResponse(
                success=False,
                error_message=f"not waiting for auth otp: {stage or 'idle'}",
                stage=stage or "idle",
            )
        except Exception as e:
            return gopay_app_pb2.AuthCompleteResponse(success=False, error_message=str(e))

    def CheckTokenValid(self, request, context):
        try:
            state = load_state()
            result = check_token_valid(state)
            state = load_state()
            return gopay_app_pb2.CheckTokenValidResponse(
                success=bool(result.get("success")),
                error_message=result.get("error", ""),
                stage=state.get("stage", "idle"),
                phone=state.get("phone", ""),
                token_valid=bool(result.get("token_valid")),
                refreshed=bool(result.get("refreshed")),
                balance_amount=int(result.get("balance_amount") or state.get("balance_amount") or 0),
                has_min_balance=bool(result.get("has_min_balance")),
                balance_currency=result.get("balance_currency") or state.get("balance_currency", ""),
            )
        except Exception as e:
            return gopay_app_pb2.CheckTokenValidResponse(success=False, error_message=str(e))

    def Unlink(self, request, context):
        """解绑后标记 token 已消费；不会自动触发下一轮。"""
        try:
            state = load_state()
            c = _client(state)
            result = run_linked_app_unlink(c, LinkedAppUnlinkOptions())
            if not result.get("success"):
                return gopay_app_pb2.UnlinkResponse(
                    success=False,
                    error_message=result.get("error_message", "unlink failed"),
                )
            state["stage"] = "consumed"
            state.pop("last_error", None)
            save_state(state)
            return gopay_app_pb2.UnlinkResponse(
                success=True,
                unlinked_count=int(result.get("unlinked_count") or 0),
            )
        except Exception as e:
            return gopay_app_pb2.UnlinkResponse(success=False, error_message=str(e))

    def Status(self, request, context):
        state = load_state()
        changed = expire_login_if_needed(state) or expire_signup_if_needed(state)
        if changed:
            save_state(state)
            state = load_state()
        if state.get("stage") == "ready":
            ensure_access_token(state)
            state = load_state()
        device = state.get("device", {})
        fp_parts = [
            device.get("profile_id", ""),
            device.get("x-phonemake", ""),
            device.get("x-phonemodel", ""),
            device.get("x-uniqueid", ""),
        ] if device else []
        fp = "/".join(str(part) for part in fp_parts if part)
        error_message = state.get("last_error", "")
        if state.get("last_token_refresh_error") and not _has_account_token(state):
            error_message = state.get("last_token_refresh_error")
        return gopay_app_pb2.StatusResponse(
            stage=state.get("stage", "idle"),
            phone=state.get("phone", ""),
            device_fingerprint=fp,
            deactivated_at=state.get("deactivated_at", 0),
            error_message=error_message,
            token_present=_has_account_token(state),
            login_otp_sent_at_unix=int(state.get("_login_otp_sent_at") or 0),
            login_otp_expires_at_unix=int(state.get("_login_otp_expires_at") or 0),
            signup_otp_sent_at_unix=int(state.get("_signup_otp_sent_at") or 0),
            signup_otp_expires_at_unix=int(state.get("_signup_otp_expires_at") or 0),
            signup_pin_otp_sent_at_unix=int(state.get("_signup_pin_otp_sent_at") or 0),
            signup_pin_otp_expires_at_unix=int(state.get("_signup_pin_otp_expires_at") or 0),
            balance_amount=int(state.get("balance_amount") or 0),
            has_min_balance=bool(state.get("has_min_balance")),
            balance_currency=state.get("balance_currency", ""),
        )

    def GetQrId(self, request, context):
        try:
            state = load_state()
            ensure_access_token(state)
            state = load_state()
            result = get_qr_id(state)
            return gopay_app_pb2.GetQrIdResponse(
                success=bool(result.get("success")),
                error_message=result.get("error", ""),
                qr_id=result.get("qr_id", ""),
                state_json=_state_json(state),
            )
        except Exception as e:
            return gopay_app_pb2.GetQrIdResponse(success=False, error_message=str(e))

    def GetReadyAccountToken(self, request, context):
        state = load_state()
        if state.get("stage") == "ready":
            token_check = check_token_valid(state)
            state = load_state()
            if not token_check.get("success") or not token_check.get("token_valid"):
                return gopay_app_pb2.GetReadyAccountTokenResponse(
                    success=False,
                    error_message=token_check.get("error", "token validation failed"),
                )
            if not token_check.get("has_min_balance"):
                return gopay_app_pb2.GetReadyAccountTokenResponse(
                    success=False,
                    error_message=_token_check_error(token_check),
                )
        token = str(state.get("token") or "").strip()
        if state.get("stage") != "ready" or not token:
            return gopay_app_pb2.GetReadyAccountTokenResponse(
                success=False,
                error_message=f"account token not ready: stage={state.get('stage', 'idle')}",
            )
        if not access_token_usable(state, 0):
            expires_at = access_token_expires_at(token)
            return gopay_app_pb2.GetReadyAccountTokenResponse(
                success=False,
                error_message=f"account token expired: expires_at={expires_at}",
            )
        return gopay_app_pb2.GetReadyAccountTokenResponse(
            success=True,
            account_token=token,
            phone=state.get("phone", ""),
        )

    def CheckDeactivation(self, request, context):
        state = load_state()
        deactivated_at = state.get("deactivated_at", 0)
        if not deactivated_at:
            return gopay_app_pb2.CheckDeactivationResponse(completed=False, remaining_seconds=-1)
        return gopay_app_pb2.CheckDeactivationResponse(completed=True, remaining_seconds=0)


for _method_name in (
    "ChangePhoneStart",
    "ChangePhoneRetry",
    "ChangePhoneComplete",
    "DeactivateStart",
    "DeactivateComplete",
    "LoginStart",
    "LoginComplete",
    "SignupStart",
    "SignupRetry",
    "SignupComplete",
    "CreatePinStart",
    "CreatePinRetry",
    "CreatePinComplete",
    "AuthStart",
    "AuthComplete",
    "CheckTokenValid",
    "ClaimEnvelope",
    "GetQrId",
    "Unlink",
    "Status",
    "GetReadyAccountToken",
    "CheckDeactivation",
    "ReplayLinkPayment",
):
    setattr(
        GopayAppServicer,
        _method_name,
        _stateful_rpc(getattr(GopayAppServicer, _method_name)),
    )


def serve():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    gopay_app_pb2_grpc.add_GopayAppServiceServicer_to_server(GopayAppServicer(), server)
    server.add_insecure_port(f"0.0.0.0:{PORT}")
    server.start()
    print(f"[gopay-app] gRPC listening on :{PORT}, explicit steps enabled")
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
