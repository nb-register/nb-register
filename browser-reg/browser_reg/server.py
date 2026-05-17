"""
Stateless-facing gRPC Browser Registration Service.

The service owns at most one in-flight browser flow. OTP is not fetched here:
StartRegister creates a browser flow and returns its flow_id immediately. The
orchestrator waits on GetFlowStatus until the browser reaches OTP, then waits
for OTP elsewhere and calls CompleteRegister with the code.
"""

from __future__ import annotations

import logging
import os
import signal
import threading
import time
import uuid
from concurrent import futures

import grpc

import browser_pb2
import browser_pb2_grpc
from browser_reg.flow import browser_register
from browser_reg.login_flow import browser_login
from browser_reg.sensitive import redact_email, sanitize_text

logger = logging.getLogger(__name__)


class BrowserFlow:
    def __init__(self, request, shutdown_event: threading.Event, mode: str = "register"):
        self.flow_id = uuid.uuid4().hex
        self.mode = mode
        self.job_id = request.job_id
        self.email = request.assigned_email
        self.password = request.password
        self.proxy = os.environ.get("PROXY_URL", "").strip()
        self.first_name = request.first_name
        self.last_name = request.last_name
        self.birthday = request.birthday
        self.safe_email = redact_email(request.assigned_email)
        self._shutdown_event = shutdown_event
        self._cancel_event = threading.Event()
        self._otp_required_event = threading.Event()
        self._otp_event = threading.Event()
        self._done_event = threading.Event()
        self._lock = threading.Lock()
        self._otp = ""
        self.started_at_unix = 0
        self.updated_at_unix = int(time.time())
        self.otp_issued_after_unix = 0
        self.otp_wait_started_at_unix = 0
        self.otp_request_action_started_at_unix = 0
        self.stage = "queued"
        self.status_message = "browser flow queued"
        self.result: dict | None = None
        self.error: Exception | None = None
        self.thread = threading.Thread(target=self._run, name=f"browser-{mode}-{self.flow_id[:8]}", daemon=True)

    @property
    def otp_required(self) -> bool:
        return self._otp_required_event.is_set()

    @property
    def done(self) -> bool:
        return self._done_event.is_set()

    def start(self) -> None:
        self.started_at_unix = int(time.time())
        self._set_status("starting", "starting browser flow")
        self.thread.start()

    def cancel(self) -> None:
        self._set_status("cancelled", f"browser {self.mode} cancelled")
        self._cancel_event.set()
        self._otp_event.set()

    def wait_for_otp_required_or_done(self, context) -> None:
        while not self._otp_required_event.is_set() and not self._done_event.is_set():
            if self._shutdown_event.is_set() or not context.is_active():
                self.cancel()
                break
            time.sleep(0.25)

    def complete(self, otp: str, context) -> None:
        with self._lock:
            self._otp = otp.strip()
        self._otp_event.set()
        while not self._done_event.is_set():
            if self._shutdown_event.is_set() or not context.is_active():
                self.cancel()
                break
            time.sleep(0.25)

    def join(self, timeout: float = 10) -> None:
        self.thread.join(timeout=timeout)

    def _should_cancel(self) -> bool:
        return self._shutdown_event.is_set() or self._cancel_event.is_set()

    def _set_status(self, stage: str, message: str = "") -> None:
        stage = str(stage or "").strip().lower() or "running"
        with self._lock:
            self.stage = stage
            self.status_message = str(message or stage).strip()
            self.updated_at_unix = int(time.time())

    def _mark_otp_request_action_started(self) -> int:
        now = int(time.time())
        with self._lock:
            if self.otp_request_action_started_at_unix <= 0:
                self.otp_request_action_started_at_unix = now
            if self.otp_issued_after_unix <= 0:
                self.otp_issued_after_unix = self.otp_request_action_started_at_unix
            return self.otp_issued_after_unix

    def _mark_otp_wait_started(self) -> tuple[int, int]:
        now = int(time.time())
        with self._lock:
            self.otp_wait_started_at_unix = now
            if self.otp_issued_after_unix <= 0 and self.otp_request_action_started_at_unix > 0:
                self.otp_issued_after_unix = self.otp_request_action_started_at_unix
            return self.otp_issued_after_unix, self.otp_wait_started_at_unix

    def _wait_for_otp(self, timeout: int | None = None) -> str:
        del timeout
        issued_after, wait_started = self._mark_otp_wait_started()
        self._otp_required_event.set()
        self._set_status("waiting_for_otp", "waiting for orchestrator-supplied OTP")
        logger.info(
            "[browser-reg] waiting orchestrator-supplied OTP flow=%s email=%s issued_after_unix=%s wait_started_at_unix=%s",
            self.flow_id,
            self.safe_email,
            issued_after,
            wait_started,
        )
        while not self._otp_event.wait(0.25):
            if self._should_cancel():
                raise RuntimeError(f"browser {self.mode} cancelled")

        if self._should_cancel():
            raise RuntimeError(f"browser {self.mode} cancelled")

        with self._lock:
            otp = self._otp.strip()
            self._otp = ""
        if not otp:
            raise RuntimeError("OTP is empty")
        logger.info("[browser-reg] received orchestrator-supplied OTP flow=%s", self.flow_id)
        self._set_status("otp_received", "received orchestrator-supplied OTP")
        return otp

    def _on_status_change(self, status_str: str) -> None:
        status = str(status_str or "").strip()
        if status == "OTP_REQUEST_CLICK":
            issued_after = self._mark_otp_request_action_started()
            self._set_status("otp_request_click", "clicking OTP request action")
            logger.info(
                "[browser-reg] OTP request action flow=%s email=%s issued_after_unix=%s",
                self.flow_id,
                self.safe_email,
                issued_after,
            )
            return
        if status == "OTP_REQUEST_CLICKED":
            self._set_status("otp_request_clicked", "OTP request action clicked")
            return
        if status == "WAITING_FOR_OTP":
            issued_after, wait_started = self._mark_otp_wait_started()
            self._set_status("waiting_for_otp", "waiting for orchestrator-supplied OTP")
            logger.info(
                "[browser-reg] browser reached OTP page flow=%s email=%s issued_after_unix=%s wait_started_at_unix=%s",
                self.flow_id,
                self.safe_email,
                issued_after,
                wait_started,
            )
            return
        if status:
            self._set_status(status, status.lower().replace("_", " "))

    def _run(self) -> None:
        try:
            self._set_status("running", "browser flow running")
            if self.mode == "login":
                self.result = browser_login(
                    email=self.email,
                    password=self.password,
                    proxy=self.proxy,
                    wait_for_otp_fn=self._wait_for_otp,
                    on_status_change_fn=self._on_status_change,
                    should_cancel_fn=self._should_cancel,
                )
            else:
                self.result = browser_register(
                    email=self.email,
                    password=self.password,
                    proxy=self.proxy,
                    wait_for_otp_fn=self._wait_for_otp,
                    on_status_change_fn=self._on_status_change,
                    first_name=self.first_name,
                    last_name=self.last_name,
                    birthday=self.birthday,
                    should_cancel_fn=self._should_cancel,
                )
            self._set_status("succeeded", f"browser {self.mode} completed")
        except Exception as exc:
            logger.warning("[browser-reg] Flow failed flow=%s error=%s", self.flow_id, sanitize_text(exc))
            self.error = exc
            if self._should_cancel():
                self._set_status("cancelled", f"browser {self.mode} cancelled")
            else:
                self._set_status("failed", sanitize_text(exc)[:500])
        finally:
            self._done_event.set()


class BrowserRegistrationServicer(browser_pb2_grpc.BrowserRegistrationServicer):
    """gRPC servicer for split browser registration."""

    def __init__(self, shutdown_event: threading.Event):
        self._shutdown_event = shutdown_event
        self._lock = threading.Lock()
        self._flows: dict[str, BrowserFlow] = {}
        logger.info("[gRPC] BrowserRegistrationServicer ready")

    def _drop_flow(self, flow_id: str) -> None:
        with self._lock:
            self._flows.pop(flow_id, None)

    def _new_flow(self, request, mode: str = "register") -> BrowserFlow | None:
        with self._lock:
            for flow_id, flow in list(self._flows.items()):
                if flow.done:
                    self._flows.pop(flow_id, None)
            if self._flows:
                return None
            flow = BrowserFlow(request, self._shutdown_event, mode=mode)
            self._flows[flow.flow_id] = flow
            return flow

    def _get_flow(self, flow_id: str) -> BrowserFlow | None:
        with self._lock:
            return self._flows.get(flow_id)

    def _start_response_from_flow(self, flow: BrowserFlow) -> browser_pb2.StartRegisterResponse:
        with flow._lock:
            stage = flow.stage
            status_message = flow.status_message
        return browser_pb2.StartRegisterResponse(
            success=True,
            error_message="",
            flow_id=flow.flow_id,
            otp_required=flow.otp_required,
            otp_issued_after_unix=flow.otp_issued_after_unix,
            stage=stage,
            status_message=status_message,
            otp_wait_started_at_unix=flow.otp_wait_started_at_unix,
            otp_request_action_started_at_unix=flow.otp_request_action_started_at_unix,
        )

    @staticmethod
    def _register_response_from_flow(flow: BrowserFlow) -> browser_pb2.RegisterResponse:
        if not flow.done:
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="browser flow did not complete",
            )
        if flow.error is not None:
            return browser_pb2.RegisterResponse(
                success=False,
                error_message=sanitize_text(flow.error)[:500],
            )
        if flow.result is None:
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="browser registration completed without result",
            )
        result = flow.result
        return browser_pb2.RegisterResponse(
            success=True,
            error_message="",
            session_token=result.get("session_token", ""),
            access_token=result.get("access_token", ""),
            device_id=result.get("device_id", ""),
            plus_trial_eligible=bool(result.get("plus_trial", False)),
            checkout_url=result.get("checkout_url", ""),
            plus_trial_checked=bool(result.get("plus_trial_checked", False)),
        )

    def _status_response_from_flow(self, flow: BrowserFlow) -> browser_pb2.BrowserFlowStatusResponse:
        with flow._lock:
            stage = flow.stage
            status_message = flow.status_message
            updated_at_unix = flow.updated_at_unix
            otp_issued_after_unix = flow.otp_issued_after_unix
            otp_wait_started_at_unix = flow.otp_wait_started_at_unix
            otp_request_action_started_at_unix = flow.otp_request_action_started_at_unix
        result = self._register_response_from_flow(flow) if flow.done else None
        success = bool(result and result.success)
        error_message = ""
        if result is not None and not result.success:
            error_message = result.error_message
        response = browser_pb2.BrowserFlowStatusResponse(
            found=True,
            flow_id=flow.flow_id,
            mode=flow.mode,
            stage=stage,
            status_message=status_message,
            otp_required=flow.otp_required,
            done=flow.done,
            success=success,
            error_message=error_message,
            started_at_unix=flow.started_at_unix,
            updated_at_unix=updated_at_unix,
            otp_issued_after_unix=otp_issued_after_unix,
            otp_wait_started_at_unix=otp_wait_started_at_unix,
            otp_request_action_started_at_unix=otp_request_action_started_at_unix,
        )
        if result is not None:
            response.result.CopyFrom(result)
        return response

    def StartRegister(self, request, context):
        safe_email = redact_email(request.assigned_email)
        flow = self._new_flow(request, mode="register")
        if flow is None:
            return browser_pb2.StartRegisterResponse(
                success=False,
                error_message="another browser registration flow is already active",
            )

        logger.info("[browser-reg] StartRegister job=%s flow=%s email=%s", request.job_id, flow.flow_id, safe_email)
        flow.start()
        if not context.is_active():
            flow.cancel()
            flow.join(timeout=2)
            self._drop_flow(flow.flow_id)
            return browser_pb2.StartRegisterResponse(
                success=False,
                error_message="browser registration cancelled",
                flow_id=flow.flow_id,
            )
        return self._start_response_from_flow(flow)

    def GetFlowStatus(self, request, context):
        del context
        flow = self._get_flow(request.flow_id)
        if flow is None:
            return browser_pb2.BrowserFlowStatusResponse(
                found=False,
                flow_id=request.flow_id,
                error_message="browser flow not found",
            )
        return self._status_response_from_flow(flow)

    def CompleteRegister(self, request, context):
        flow = self._get_flow(request.flow_id)
        if flow is None:
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="browser registration flow not found",
            )
        if not request.otp.strip():
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="otp is required",
            )

        logger.info("[browser-reg] CompleteRegister flow=%s", request.flow_id)
        flow.complete(request.otp, context)
        flow.join(timeout=1)
        response = self._register_response_from_flow(flow)
        self._drop_flow(request.flow_id)
        logger.info("[browser-reg] CompleteRegister done flow=%s success=%s", request.flow_id, response.success)
        return response

    def CancelRegister(self, request, context):
        del context
        flow = self._get_flow(request.flow_id)
        if flow is None:
            return browser_pb2.CancelRegisterResponse(success=True, error_message="")
        logger.info("[browser-reg] CancelRegister flow=%s", request.flow_id)
        flow.cancel()
        flow.join()
        self._drop_flow(request.flow_id)
        return browser_pb2.CancelRegisterResponse(success=True, error_message="")

    def StartLogin(self, request, context):
        safe_email = redact_email(request.assigned_email)
        flow = self._new_flow(request, mode="login")
        if flow is None:
            return browser_pb2.StartRegisterResponse(
                success=False,
                error_message="another browser registration flow is already active",
            )

        logger.info("[browser-reg] StartLogin job=%s flow=%s email=%s", request.job_id, flow.flow_id, safe_email)
        flow.start()
        if not context.is_active():
            flow.cancel()
            flow.join(timeout=2)
            self._drop_flow(flow.flow_id)
            return browser_pb2.StartRegisterResponse(
                success=False,
                error_message="browser login cancelled",
                flow_id=flow.flow_id,
            )
        return self._start_response_from_flow(flow)


    def CompleteLogin(self, request, context):
        flow = self._get_flow(request.flow_id)
        if flow is None:
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="browser login flow not found",
            )
        if not request.otp.strip():
            return browser_pb2.RegisterResponse(
                success=False,
                error_message="otp is required",
            )

        logger.info("[browser-reg] CompleteLogin flow=%s", request.flow_id)
        flow.complete(request.otp, context)
        flow.join(timeout=1)
        response = self._register_response_from_flow(flow)
        self._drop_flow(request.flow_id)
        logger.info("[browser-reg] CompleteLogin done flow=%s success=%s", request.flow_id, response.success)
        return response

    def CancelLogin(self, request, context):
        del context
        flow = self._get_flow(request.flow_id)
        if flow is None:
            return browser_pb2.CancelRegisterResponse(success=True, error_message="")
        logger.info("[browser-reg] CancelLogin flow=%s", request.flow_id)
        flow.cancel()
        flow.join()
        self._drop_flow(request.flow_id)
        return browser_pb2.CancelRegisterResponse(success=True, error_message="")


def serve(grpc_port: int = 50051):
    """Start the gRPC registration service."""

    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=1),
        options=[
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
    )
    shutdown_event = threading.Event()
    browser_pb2_grpc.add_BrowserRegistrationServicer_to_server(
        BrowserRegistrationServicer(shutdown_event),
        server,
    )
    server.add_insecure_port(f"0.0.0.0:{grpc_port}")
    server.start()
    logger.info("[server] gRPC server listening on :%s (workers=1)", grpc_port)

    def request_shutdown(signum, _frame):
        logger.info("[server] Signal %s received, stopping gRPC server ...", signum)
        shutdown_event.set()
        server.stop(grace=10)

    signal.signal(signal.SIGTERM, request_shutdown)
    signal.signal(signal.SIGINT, request_shutdown)

    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("[server] Shutting down ...")
        shutdown_event.set()
        server.stop(grace=5)
