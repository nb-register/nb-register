from __future__ import annotations

import logging
import os
import signal
import threading
from concurrent import futures
from urllib.parse import urlparse

import grpc

import mailbox_register_pb2
import mailbox_register_pb2_grpc
import register_provider


DEFAULT_LISTEN_ADDR = ":50051"

logger = logging.getLogger("outlook-register-service")


def env_str(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def grpc_listen_addr(value: str) -> str:
    value = value or DEFAULT_LISTEN_ADDR
    if value.startswith(":"):
        return "[::]" + value
    parsed = urlparse("//" + value)
    if parsed.hostname and parsed.port:
        return value
    if parsed.hostname and not parsed.port:
        return value + ":50051"
    return value


class MailboxRegistrationService(mailbox_register_pb2_grpc.MailboxRegistrationServiceServicer):
    def RunMailboxRegistration(self, request, context):
        del context
        try:
            result = register_provider.run_registration_request(
                enabled=bool(request.enabled),
                import_only=bool(request.import_only),
            )
            accounts = [
                mailbox_register_pb2.MailboxRegistrationAccount(
                    email_address=item.get("email_address", ""),
                    password=item.get("password", ""),
                    refresh_token=item.get("refresh_token", ""),
                    access_token=item.get("access_token", ""),
                    source=item.get("source", ""),
                )
                for item in result.get("accounts", [])
            ]
            success = bool(result.get("success")) and len(accounts) > 0
            error_message = result.get("error_message", "")
            if not success and not error_message:
                error_message = "mailbox registration returned no accounts"
            return mailbox_register_pb2.RunMailboxRegistrationResponse(
                success=success,
                exit_code=int(result.get("exit_code", 1)),
                error_message=error_message,
                accounts=accounts,
            )
        except Exception as err:
            logger.exception("mailbox registration failed")
            return mailbox_register_pb2.RunMailboxRegistrationResponse(
                success=False,
                exit_code=1,
                error_message=str(err),
            )

    def RunMailboxOAuth(self, request, context):
        del context
        try:
            accounts = [
                {
                    "email_address": account.email_address,
                    "password": account.password,
                    "refresh_token": account.refresh_token,
                    "access_token": account.access_token,
                    "source": account.source,
                }
                for account in request.accounts
            ]
            result = register_provider.run_oauth(
                email_address=request.email_address,
                only_missing=request.only_missing,
                limit=request.limit,
                accounts=accounts,
            )
            results = [
                mailbox_register_pb2.MailboxOAuthResult(
                    email_address=item.get("email_address", ""),
                    success=bool(item.get("success")),
                    error_message=item.get("error_message", ""),
                    refresh_token=item.get("refresh_token", ""),
                    access_token=item.get("access_token", ""),
                )
                for item in result.get("results", [])
            ]
            return mailbox_register_pb2.RunMailboxOAuthResponse(
                success=bool(result.get("success")),
                processed=int(result.get("processed", 0)),
                succeeded=int(result.get("succeeded", 0)),
                failed=int(result.get("failed", 0)),
                error_message=result.get("error_message", ""),
                results=results,
            )
        except Exception as err:
            logger.exception("mailbox OAuth failed")
            return mailbox_register_pb2.RunMailboxOAuthResponse(success=False, error_message=str(err))


def serve() -> None:
    register_provider.configure_logging()
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    mailbox_register_pb2_grpc.add_MailboxRegistrationServiceServicer_to_server(
        MailboxRegistrationService(),
        server,
    )
    listen_addr = grpc_listen_addr(env_str("LISTEN_ADDR", DEFAULT_LISTEN_ADDR))
    server.add_insecure_port(listen_addr)
    server.start()
    logger.info("Starting Outlook mailbox registration gRPC server on %s", listen_addr)

    stop_event = threading.Event()

    def stop(signum, frame):
        del frame
        logger.info("received signal %s; stopping", signum)
        server.stop(5)
        stop_event.set()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    stop_event.wait()


if __name__ == "__main__":
    serve()
