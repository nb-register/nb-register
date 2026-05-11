#!/usr/bin/env python3
"""Segmented gRPC wrapper for the GoPay payment flow."""

from __future__ import annotations

import argparse
import copy
import json
import logging
import os
import re
import threading
import time
import uuid
from concurrent import futures
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, urlparse

import grpc

import otp_pb2
import otp_pb2_grpc
import payment_pb2
import payment_pb2_grpc
from gopay import (
    DEFAULT_STRIPE_PK,
    GoPayCharger,
    GoPayError,
    OTPCancelled,
    _build_chatgpt_session,
    _extract_otp_from_payload,
    _load_cfg,
    probe_plus_trial_checkout,
    resolve_gopay_cfg,
    resolve_proxy,
    validate_gopay_cfg,
)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%H:%M:%S",
)
logger = logging.getLogger(__name__)

GOPAY_OTP_SOURCE_HINTS = ("whatsapp", "gopay", "go pay", "gojek")


def _billing_from_config(cfg: dict[str, Any]) -> dict[str, Any]:
    billing = cfg.get("billing") or {}
    if billing:
        return dict(billing)
    cards = cfg.get("cards") or []
    if cards and isinstance(cards[0], dict):
        card0 = cards[0]
        out = dict(card0.get("address") or {})
        for key in ("name", "email"):
            if card0.get(key):
                out[key] = card0[key]
        return out
    return {}


def _normalize_listen(value: str) -> str:
    value = (value or ":50051").strip()
    if value.startswith(":"):
        return "[::]" + value
    return value


def _parse_http_listen(value: str) -> tuple[str, int] | None:
    value = (value or "").strip()
    if not value or value.lower() in {"off", "false", "disabled"}:
        return None
    if value.startswith(":"):
        return "0.0.0.0", int(value[1:])
    host, sep, port = value.rpartition(":")
    if not sep:
        return "0.0.0.0", int(value)
    return host or "0.0.0.0", int(port)


def _close_session(session: Any) -> None:
    close = getattr(session, "close", None)
    if callable(close):
        try:
            close()
        except Exception:
            pass


def _looks_access_token(value: str) -> bool:
    # OpenAI access tokens are JWTs. NextAuth session tokens are JWE-like and
    # normally have a different dot count, so this is only a compatibility shim.
    return value.count(".") == 2


@dataclass
class PendingFlow:
    charger: GoPayCharger
    state: dict[str, Any]

    def close(self) -> None:
        self.charger.close()


class FlowStore:
    def __init__(self):
        self._lock = threading.Lock()
        self._flows: dict[str, PendingFlow] = {}

    def put(self, charger: GoPayCharger, state: dict[str, Any]) -> str:
        flow_id = uuid.uuid4().hex
        with self._lock:
            self._flows[flow_id] = PendingFlow(charger=charger, state=state)
        return flow_id

    def pop(self, flow_id: str) -> PendingFlow | None:
        with self._lock:
            return self._flows.pop(flow_id, None)

    def close(self) -> None:
        with self._lock:
            flows = list(self._flows.values())
            self._flows.clear()
        for flow in flows:
            flow.close()


class OtpStore:
    def __init__(self):
        self._cond = threading.Condition()
        self._items: list[dict[str, Any]] = []

    def submit(
        self,
        otp: str,
        source: str = "webhook",
        issued_at_unix: int | None = None,
        hint: str = "",
    ) -> None:
        code = re.sub(r"\D", "", str(otp or ""))
        if not re.fullmatch(r"\d{4,8}", code):
            raise ValueError("otp must be 4-8 digits")
        source = str(source or "webhook")[:80]
        hint = str(hint or "")[:512]
        ts = int(issued_at_unix or time.time())
        with self._cond:
            self._items.append({"otp": code, "source": source, "ts": ts, "hint": hint})
            if len(self._items) > 20:
                self._items = self._items[-20:]
            self._cond.notify_all()

    def wait(
        self,
        timeout_seconds: int,
        issued_after_unix: int,
        is_active,
        purpose: str = "gopay",
    ) -> dict[str, Any] | None:
        deadline = time.time() + max(1, int(timeout_seconds))
        issued_after_unix = int(issued_after_unix or 0)
        purpose = (purpose or "gopay").strip().lower()
        with self._cond:
            while is_active():
                while self._items:
                    item = self._items.pop(0)
                    if int(item["ts"]) >= issued_after_unix:
                        if _otp_matches_purpose(item, purpose):
                            return item
                        logger.info(
                            "[payment] ignoring OTP source=%s for purpose=%s",
                            str(item.get("source") or "")[:80],
                            purpose,
                        )
                remaining = deadline - time.time()
                if remaining <= 0:
                    return None
                self._cond.wait(timeout=min(1.0, remaining))
        return None


class OtpService(otp_pb2_grpc.OtpServiceServicer):
    def __init__(self, otp_store: OtpStore):
        self._otp_store = otp_store

    def WaitForOtp(self, request, context):
        timeout_seconds = max(1, int(request.timeout_seconds or 60))
        issued_after_unix = int(request.issued_after_unix or 0)
        purpose = (request.purpose or "gopay").strip() or "gopay"
        logger.info(
            "[payment] WaitForOtp via built-in webhook purpose=%s timeout=%ss issued_after=%s",
            purpose,
            timeout_seconds,
            issued_after_unix,
        )
        item = self._otp_store.wait(
            timeout_seconds=timeout_seconds,
            issued_after_unix=issued_after_unix,
            is_active=context.is_active,
            purpose=purpose,
        )
        if item:
            logger.info("[payment] OTP served from built-in webhook source=%s", item["source"])
            return otp_pb2.WaitForOtpResponse(
                found=True,
                otp=str(item["otp"]),
                source=str(item["source"]),
                error_message="",
            )
        if not context.is_active():
            return otp_pb2.WaitForOtpResponse(found=False, error_message="otp wait cancelled")
        return otp_pb2.WaitForOtpResponse(
            found=False,
            error_message=f"timeout waiting for OTP after {timeout_seconds}s",
        )


def _payload_source(payload: Any, headers) -> str:
    if isinstance(payload, dict):
        for key in ("source", "from", "sender", "app", "appName", "packageName", "title"):
            value = str(payload.get(key) or "").strip()
            if value:
                return value
    return str(headers.get("User-Agent") or "webhook").strip() or "webhook"


def _payload_hint(payload: Any) -> str:
    if isinstance(payload, str):
        return payload[:512]
    try:
        return json.dumps(payload, ensure_ascii=False)[:512]
    except Exception:
        return str(payload)[:512]


def _otp_matches_purpose(item: dict[str, Any], purpose: str) -> bool:
    if purpose not in {"gopay", "payment", "gopay_payment"}:
        return True
    source = str(item.get("source") or "").lower()
    haystack = f"{source} {item.get('hint') or ''}".lower()
    return any(hint in haystack for hint in GOPAY_OTP_SOURCE_HINTS)


def _single_value_query(query: dict[str, list[str]]) -> dict[str, str]:
    return {key: str(values[0]) for key, values in query.items() if values}


def _make_otp_webhook_handler(otp_store: OtpStore):
    class OtpWebhookHandler(BaseHTTPRequestHandler):
        server_version = "GoPayOtpWebhook/1.0"
        allowed_paths = {"/otp", "/webhook", "/webhook/otp", "/gopay/otp"}

        def log_message(self, fmt, *args):
            logger.info("[payment-webhook] " + fmt, *args)

        def do_GET(self):
            parsed = urlparse(self.path)
            if parsed.path in {"/healthz", "/health"}:
                self._json(200, {"ok": True})
                return
            if parsed.path in self.allowed_paths:
                self._handle_submit(_single_value_query(parse_qs(parsed.query)))
                return
            self._json(404, {"ok": False, "error": "not found"})

        def do_POST(self):
            parsed = urlparse(self.path)
            if parsed.path not in self.allowed_paths:
                self._json(404, {"ok": False, "error": "not found"})
                return

            length = min(int(self.headers.get("Content-Length") or "0"), 65536)
            raw = self.rfile.read(length).decode("utf-8", errors="replace")
            payload: Any = raw
            if "json" in str(self.headers.get("Content-Type") or "").lower():
                try:
                    payload = json.loads(raw or "{}")
                except json.JSONDecodeError:
                    payload = raw

            self._handle_submit(payload)

        def _handle_submit(self, payload: Any):
            code = _extract_otp_from_payload(payload)
            if not code:
                source = _payload_source(payload, self.headers)
                logger.info("[payment] notification accepted without OTP source=%s", source[:80])
                self._json(200, {"ok": True, "accepted": False, "message": "otp not found"})
                return

            source = _payload_source(payload, self.headers)
            try:
                otp_store.submit(code, source=source, hint=_payload_hint(payload))
            except ValueError as exc:
                self._json(422, {"ok": False, "error": str(exc)})
                return

            logger.info("[payment] OTP accepted from webhook source=%s", source[:80])
            self._json(200, {"ok": True, "accepted": True})

        def _json(self, status: int, payload: dict[str, Any]):
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    return OtpWebhookHandler


class OtpWebhookServer:
    def __init__(self, listen: str, otp_store: OtpStore):
        parsed = _parse_http_listen(listen)
        if parsed is None:
            self._server = None
            self._thread = None
            return
        handler = _make_otp_webhook_handler(otp_store)
        self._server = ThreadingHTTPServer(parsed, handler)
        self._thread = threading.Thread(target=self._server.serve_forever, name="payment-otp-webhook", daemon=True)

    def start(self) -> None:
        if self._server is None or self._thread is None:
            logger.info("[payment] OTP webhook disabled")
            return
        host, port = self._server.server_address[:2]
        self._thread.start()
        logger.info("[payment] OTP webhook listening on %s:%s", host, port)

    def close(self) -> None:
        if self._server is None:
            return
        self._server.shutdown()
        self._server.server_close()


class PaymentService(payment_pb2_grpc.PaymentServiceServicer):
    def __init__(self, cfg: dict[str, Any]):
        self._cfg = cfg
        self._flows = FlowStore()

    def close(self) -> None:
        self._flows.close()

    def ProbePlusTrial(self, request, context):
        session_token = (request.session_token or "").strip()
        access_token = (getattr(request, "access_token", "") or "").strip()
        if session_token and not access_token and _looks_access_token(session_token):
            access_token = session_token
            session_token = ""

        if not (session_token or access_token):
            return payment_pb2.ProbePlusTrialPaymentResponse(
                success=False,
                error_message="session_token or access_token is required",
            )

        cs_session = None
        try:
            cfg = copy.deepcopy(self._cfg)
            fresh_checkout = cfg.get("fresh_checkout") or {}
            auth_cfg = dict(fresh_checkout.get("auth") or {})
            auth_cfg.pop("cookie_header", None)
            auth_cfg.pop("session_token", None)
            auth_cfg.pop("access_token", None)
            if session_token:
                auth_cfg["session_token"] = session_token
            if access_token:
                auth_cfg["access_token"] = access_token

            proxy = resolve_proxy(cfg)
            cs_session = _build_chatgpt_session(auth_cfg, proxy=proxy)
            stripe_pk = (
                (cfg.get("stripe") or {}).get("publishable_key")
                or auth_cfg.get("stripe_pk")
                or DEFAULT_STRIPE_PK
            )

            logger.info("[payment] ProbePlusTrial start")
            result = probe_plus_trial_checkout(
                cs_session,
                stripe_pk=stripe_pk,
                runtime_cfg=dict(cfg.get("runtime") or {}),
                proxy=proxy,
                log=logger.info,
            )
            logger.info(
                "[payment] ProbePlusTrial checked=%s eligible=%s amount=%s source=%s",
                result.get("checked"),
                result.get("plus_trial_eligible"),
                result.get("amount"),
                result.get("source"),
            )
            return payment_pb2.ProbePlusTrialPaymentResponse(
                success=True,
                error_message=str(result.get("error_message") or "")[:500],
                checked=bool(result.get("checked")),
                plus_trial_eligible=bool(result.get("plus_trial_eligible")),
                amount=int(result.get("amount") or 0),
                currency=str(result.get("currency") or ""),
                source=str(result.get("source") or ""),
                checkout_url=str(result.get("checkout_url") or ""),
            )
        except GoPayError as exc:
            logger.error("[payment] ProbePlusTrial failed: %s", exc)
            return payment_pb2.ProbePlusTrialPaymentResponse(success=False, error_message=str(exc)[:500])
        except Exception as exc:
            logger.exception("[payment] ProbePlusTrial crashed")
            return payment_pb2.ProbePlusTrialPaymentResponse(success=False, error_message=str(exc)[:500])
        finally:
            if cs_session is not None:
                _close_session(cs_session)

    def StartGoPay(self, request, context):
        session_token = (request.session_token or "").strip()
        access_token = (getattr(request, "access_token", "") or "").strip()
        if session_token and not access_token and _looks_access_token(session_token):
            access_token = session_token
            session_token = ""

        if not (session_token or access_token):
            return payment_pb2.StartGoPayResponse(
                success=False,
                error_message="session_token or access_token is required",
            )

        charger = None
        cs_session = None
        try:
            cfg = copy.deepcopy(self._cfg)
            fresh_checkout = cfg.get("fresh_checkout") or {}
            auth_cfg = dict(fresh_checkout.get("auth") or {})
            auth_cfg.pop("cookie_header", None)
            auth_cfg.pop("session_token", None)
            auth_cfg.pop("access_token", None)
            if session_token:
                auth_cfg["session_token"] = session_token
            if access_token:
                auth_cfg["access_token"] = access_token

            proxy = resolve_proxy(cfg)
            cs_session = _build_chatgpt_session(auth_cfg, proxy=proxy)

            gopay_cfg = validate_gopay_cfg(resolve_gopay_cfg(cfg))

            stripe_pk = (
                (cfg.get("stripe") or {}).get("publishable_key")
                or auth_cfg.get("stripe_pk")
                or DEFAULT_STRIPE_PK
            )
            runtime_cfg = dict(cfg.get("runtime") or {})
            charger = GoPayCharger(
                cs_session,
                gopay_cfg,
                otp_provider=lambda: (_ for _ in ()).throw(OTPCancelled("external OTP required")),
                proxy=proxy,
                runtime_cfg=runtime_cfg,
                log=logger.info,
            )

            logger.info("[payment] StartGoPay start")
            state = charger.start_until_otp(stripe_pk=stripe_pk, billing=_billing_from_config(cfg))
            flow_id = self._flows.put(charger, state)
            charger = None
            cs_session = None
            logger.info("[payment] StartGoPay waiting_otp flow=%s", flow_id[:8])
            return payment_pb2.StartGoPayResponse(
                success=True,
                flow_id=flow_id,
                snap_token=str(state.get("snap_token") or ""),
                issued_after_unix=int(state.get("issued_after_unix") or 0),
                expires_at_unix=0,
            )
        except GoPayError as exc:
            logger.error("[payment] StartGoPay failed: %s", exc)
            return payment_pb2.StartGoPayResponse(success=False, error_message=str(exc)[:500])
        except Exception as exc:
            logger.exception("[payment] StartGoPay crashed")
            return payment_pb2.StartGoPayResponse(success=False, error_message=str(exc)[:500])
        finally:
            if charger is not None:
                charger.close()
            elif cs_session is not None:
                _close_session(cs_session)

    def CompleteGoPay(self, request, context):
        if not request.flow_id:
            return payment_pb2.GoPayResponse(success=False, error_message="flow_id is required")
        if not request.otp:
            return payment_pb2.GoPayResponse(success=False, error_message="otp is required")

        flow = self._flows.pop(request.flow_id)
        if flow is None:
            return payment_pb2.GoPayResponse(success=False, error_message="payment flow not found")

        try:
            logger.info("[payment] CompleteGoPay flow=%s", request.flow_id[:8])
            result = flow.charger.complete_after_otp(flow.state, request.otp)
            state = str(result.get("state") or "")
            success = state == "succeeded"
            return payment_pb2.GoPayResponse(
                success=success,
                error_message="" if success else f"payment state={state or 'unknown'}",
                charge_ref=str(result.get("charge_ref") or ""),
                snap_token=str(result.get("snap_token") or ""),
            )
        except GoPayError as exc:
            logger.error("[payment] CompleteGoPay failed: %s", exc)
            return payment_pb2.GoPayResponse(success=False, error_message=str(exc)[:500])
        except Exception as exc:
            logger.exception("[payment] CompleteGoPay crashed")
            return payment_pb2.GoPayResponse(success=False, error_message=str(exc)[:500])
        finally:
            flow.close()

    def CancelGoPay(self, request, context):
        if not request.flow_id:
            return payment_pb2.CancelGoPayResponse(success=False, error_message="flow_id is required")
        flow = self._flows.pop(request.flow_id)
        if flow is not None:
            flow.close()
        logger.info("[payment] CancelGoPay flow=%s found=%s", request.flow_id[:8], flow is not None)
        return payment_pb2.CancelGoPayResponse(success=True)


def serve(config_path: str, listen: str, otp_webhook_listen: str):
    cfg = _load_cfg(config_path)
    otp_store = OtpStore()
    service = PaymentService(cfg)
    webhook_server = OtpWebhookServer(otp_webhook_listen, otp_store)
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=4),
        options=[
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
    )
    payment_pb2_grpc.add_PaymentServiceServicer_to_server(service, server)
    otp_pb2_grpc.add_OtpServiceServicer_to_server(OtpService(otp_store), server)
    listen_addr = _normalize_listen(listen)
    server.add_insecure_port(listen_addr)
    server.start()
    webhook_server.start()
    logger.info("[payment] gRPC listening on %s", listen_addr)
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("[payment] shutting down")
        server.stop(grace=5)
    finally:
        webhook_server.close()
        service.close()


def main():
    parser = argparse.ArgumentParser(description="GoPay payment gRPC service")
    parser.add_argument("--config", default="config.json")
    parser.add_argument("--listen", default=":50051")
    parser.add_argument("--flow-ttl", type=int, default=0, help=argparse.SUPPRESS)
    parser.add_argument("--otp-webhook-listen", default=os.getenv("GOPAY_OTP_WEBHOOK_LISTEN", ":8081"))
    args = parser.parse_args()

    serve(
        config_path=args.config,
        listen=args.listen,
        otp_webhook_listen=args.otp_webhook_listen,
    )


if __name__ == "__main__":
    main()
