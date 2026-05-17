import base64
import json
import time
import unittest
from unittest.mock import patch

import gopay_client
import gopay_app
import replay

try:
    import app_server
except ImportError as exc:
    app_server = None
    APP_SERVER_IMPORT_ERROR = exc
else:
    APP_SERVER_IMPORT_ERROR = None


TEST_LOCAL_PHONE = "80000000000"
TEST_FULL_PHONE = "6280000000000"
TEST_E164_PHONE = f"+{TEST_FULL_PHONE}"
TEST_PIN = "000000"
TEST_CHANGE_LOCAL_PHONE = "89600000000"
TEST_CHANGE_FULL_PHONE = f"62{TEST_CHANGE_LOCAL_PHONE}"


def jwt_with_exp(exp: int) -> str:
    header = base64.urlsafe_b64encode(b'{"alg":"none"}').decode().rstrip("=")
    payload = base64.urlsafe_b64encode(json.dumps({"exp": exp}).encode()).decode().rstrip("=")
    return f"{header}.{payload}."


def jwt_with_payload(payload: dict) -> str:
    header = base64.urlsafe_b64encode(b'{"alg":"RS256","typ":"JWT"}').decode().rstrip("=")
    body = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip("=")
    return f"{header}.{body}.signature"


class GopayClientHeaderTests(unittest.TestCase):
    def test_customer_signup_uses_captured_app_header_profile(self):
        client = gopay_client.GopayClient(
            "",
            device={
                "x-uniqueid": "unique-id",
                "x-devicetoken": "device-token",
                "x-e2": "xe2",
                "m1_signature": "m1-signature",
                "m1_appsflyer_id": "appsflyer-id",
                "m1_widevine_id": "widevine-id",
            },
        )

        with patch.object(gopay_client, "generate_xe1", return_value=("xe1", "body-md5")) as sign:
            headers = client._headers(
                "POST",
                "https://api.gojekapi.com/v7/customers/signup",
                '{"client_name":"gopay:consumer:app"}',
                {
                    "Authorization": "Basic request-id",
                    "Verification-Token": "Bearer verification-token",
                },
            )

        self.assertEqual(sign.call_args.args[3], "Basic request-id")
        self.assertEqual(headers["Authorization"], "Basic request-id")
        self.assertEqual(headers["Verification-Token"], "Bearer verification-token")
        self.assertEqual(headers["X-E1"], "xe1")
        self.assertEqual(headers["X-E2"], "xe2")
        self.assertEqual(headers["X-AppId"], "com.gojek.gopay")
        self.assertEqual(headers["X-AppType"], "GOPAY")
        self.assertEqual(headers["Gojek-Country-Code"], "ID")
        self.assertEqual(headers["Content-Type"], "application/json")
        self.assertIn("X-M1", headers)
        for key in ("X-AuthSDK-Version", "X-CVSDK-Version", "X-Request-ID", "Transaction-ID", "X-Location", "X-Location-Accuracy"):
            self.assertIn(key, headers)
        self.assertNotIn("D1", headers)
        self.assertNotIn("X-E3", headers)
        self.assertNotIn("AdjTs", headers)
        self.assertNotIn("X-Session-ID", headers)


class TokenRefreshTests(unittest.TestCase):
    def test_access_token_usable_checks_exp(self):
        state = {"token": jwt_with_exp(int(time.time()) + 120)}

        self.assertTrue(gopay_app.access_token_usable(state, 30))
        self.assertFalse(gopay_app.access_token_usable(state, 180))

    def test_ensure_access_token_refreshes_near_expiry(self):
        state = {
            "token": jwt_with_exp(int(time.time()) + 60),
            "refresh_token": "refresh-token",
        }

        def fake_refresh(target):
            target["token"] = jwt_with_exp(int(time.time()) + 3600)
            return {"success": True, "refreshed": True}

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "refresh_access_token", fake_refresh):
            result = gopay_app.ensure_access_token(state, min_ttl_seconds=300)

        self.assertTrue(result["success"])
        self.assertTrue(result["refreshed"])
        self.assertTrue(gopay_app.access_token_usable(state, 300))

    def test_ensure_access_token_keeps_valid_token(self):
        state = {
            "token": jwt_with_exp(int(time.time()) + 3600),
            "refresh_token": "refresh-token",
        }

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "refresh_access_token") as refresh:
            result = gopay_app.ensure_access_token(state, min_ttl_seconds=300)

        self.assertTrue(result["success"])
        self.assertFalse(result["refreshed"])
        refresh.assert_not_called()

    def test_store_token_response_replaces_stale_refresh_token(self):
        state = {
            "token": "old-token",
            "refresh_token": "old-refresh-token",
        }

        gopay_app._store_token_response(state, {
            "access_token": jwt_with_exp(int(time.time()) + 3600),
        })

        self.assertNotEqual(state["token"], "old-token")
        self.assertNotIn("refresh_token", state)

    def test_refresh_access_token_prefers_captured_token_field(self):
        state = {"token": "access-token", "refresh_token": "refresh-token"}
        calls = []
        client_tokens = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                client_tokens.append(token)

            def post(self, url, body=None, **kwargs):
                calls.append(body)
                return {
                    "status": 201,
                    "data": {
                        "access_token": jwt_with_exp(int(time.time()) + 3600),
                        "refresh_token": "new-refresh-token",
                    },
                }

        with patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "save_state", lambda target: None):
            result = gopay_app.refresh_access_token(state)

        self.assertTrue(result["success"])
        self.assertEqual(client_tokens[0], "access-token")
        self.assertEqual(calls[0]["grant_type"], "refresh_token")
        self.assertEqual(calls[0]["token"], "refresh-token")
        self.assertNotIn("refresh_token", calls[0])

    def test_check_token_valid_checks_profile_before_refresh(self):
        state = {"token": "access-token", "device": {}}
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                calls.append(("get", self.token, url))
                if url.endswith("/v1/payment-options/balances"):
                    return {"status": 200, "data": [
                        {"type": "GOPAY_WALLET", "balance": {"value": 1, "currency": "IDR"}},
                        {"type": "DEBIT", "balance": {"value": 1, "currency": "IDR"}},
                    ], "raw": {"success": True}}
                return {"status": 200, "data": {"phone": TEST_E164_PHONE}}

        with patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "save_state", lambda target: None):
            result = gopay_app.check_token_valid(state)

        self.assertTrue(result["success"])
        self.assertTrue(result["token_valid"])
        self.assertFalse(result["refreshed"])
        self.assertTrue(result["has_min_balance"])
        self.assertEqual(result["balance_amount"], 1)
        self.assertEqual(state["stage"], "ready")
        self.assertEqual(state["phone"], TEST_LOCAL_PHONE)
        self.assertEqual(calls, [
            ("get", "access-token", "https://customer.gopayapi.com/v1/users/profile"),
            ("get", "access-token", "https://customer.gopayapi.com/v1/payment-options/balances"),
        ])

    def test_check_token_valid_refreshes_then_rechecks_profile(self):
        state = {"token": "expired-token", "refresh_token": "refresh-token", "device": {}}
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                calls.append(("get", self.token, url))
                if url.endswith("/v1/payment-options/balances"):
                    return {"status": 200, "data": [
                        {"type": "GOPAY_WALLET", "balance": {"value": 1, "currency": "IDR"}},
                    ], "raw": {"success": True}}
                if self.token == "new-token":
                    return {"status": 200, "data": {"phone": TEST_E164_PHONE}}
                return {"status": 401, "data": {"errors": [{"message": "unauthorized"}]}}

            def post(self, url, body=None, **kwargs):
                calls.append(("post", self.token, url, body))
                return {
                    "status": 201,
                    "data": {
                        "access_token": "new-token",
                        "refresh_token": "new-refresh-token",
                        "expires_in": 1500,
                    },
                }

        with patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "save_state", lambda target: None):
            result = gopay_app.check_token_valid(state)

        self.assertTrue(result["success"])
        self.assertTrue(result["token_valid"])
        self.assertTrue(result["refreshed"])
        self.assertTrue(result["has_min_balance"])
        self.assertEqual(state["token"], "new-token")
        self.assertEqual(state["refresh_token"], "new-refresh-token")
        self.assertEqual([call[0] for call in calls], ["get", "post", "get", "get"])

    def test_check_gopay_balance_uses_wallet_not_debit(self):
        state = {"token": "access-token", "device": {}}

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                pass

            def get(self, url, **kwargs):
                return {"status": 200, "data": [
                    {"type": "DEBIT", "balance": {"value": 1, "currency": "IDR"}},
                    {"type": "GOPAY_WALLET", "balance": {"value": 0, "currency": "IDR"}},
                ], "raw": {"success": True}}

        with patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "save_state", lambda target: None):
            result = gopay_app.check_gopay_balance(state)

        self.assertTrue(result["success"])
        self.assertFalse(result["has_min_balance"])
        self.assertEqual(result["balance_amount"], 0)
        self.assertEqual(state["last_error"], "INSUFFICIENT_GOPAY_BALANCE")

    def test_login_pending_expires_without_timestamp(self):
        state = {
            "stage": "login_otp_pending",
            "_login_otp_token": "otp-token",
            "_login_2fa_token": "2fa-token",
        }

        self.assertTrue(gopay_app.expire_login_if_needed(state, now=1000))
        self.assertEqual(state["stage"], "idle")
        self.assertNotIn("_login_otp_token", state)
        self.assertEqual(state["last_error"], "LOGIN_OTP_TIMEOUT")

    def test_login_pending_uses_expiry_timestamp(self):
        state = {
            "stage": "login_otp_pending",
            "_login_otp_token": "otp-token",
            "_login_2fa_token": "2fa-token",
            "_login_otp_expires_at": 2000,
        }

        self.assertFalse(gopay_app.expire_login_if_needed(state, now=1999))
        self.assertTrue(gopay_app.expire_login_if_needed(state, now=2000))
        self.assertEqual(state["stage"], "idle")

    def test_signup_pending_uses_expiry_timestamp(self):
        state = {
            "stage": "signup_otp_pending",
            "_signup_phone": TEST_LOCAL_PHONE,
            "_signup_otp_token": "otp-token",
            "_signup_otp_expires_at": 2000,
        }

        self.assertFalse(gopay_app.expire_signup_if_needed(state, now=1999))
        self.assertTrue(gopay_app.expire_signup_if_needed(state, now=2000))
        self.assertEqual(state["stage"], "idle")
        self.assertNotIn("_signup_otp_token", state)
        self.assertEqual(state["last_error"], "SIGNUP_OTP_TIMEOUT")

    def test_signup_pin_pending_uses_expiry_timestamp(self):
        state = {
            "stage": "signup_pin_otp_pending",
            "token": "token",
            "_signup_pin_otp_token": "otp-token",
            "_signup_pin_otp_expires_at": 2000,
        }

        self.assertFalse(gopay_app.expire_signup_if_needed(state, now=1999))
        self.assertTrue(gopay_app.expire_signup_if_needed(state, now=2000))
        self.assertEqual(state["stage"], "signup_pin_required")
        self.assertNotIn("_signup_pin_otp_token", state)
        self.assertEqual(state["last_error"], "SIGNUP_PIN_OTP_TIMEOUT")


class LogonProfileTests(unittest.TestCase):
    def test_new_logon_device_profile_uses_default_capture_shape(self):
        with patch.object(gopay_app, "generate_random_device_fingerprint", return_value={"x-uniqueid": "u1"}) as gen:
            profile = gopay_app.new_logon_device_profile()

        gen.assert_called_once_with()
        self.assertEqual(profile["x-uniqueid"], "u1")
        self.assertTrue(profile["profile_id"])
        self.assertTrue(profile["profile_created_at"])

    def test_random_device_fingerprint_ignores_static_identity_env(self):
        with patch.dict(gopay_client.os.environ, {
            "GOPAY_UNIQUE_ID": "fixed-unique-id",
            "GOPAY_APPSFLYER_ID": "fixed-appsflyer-id",
            "GOPAY_WIDEVINE_ID": "fixed-widevine-id",
            "GOPAY_M1_SIGNATURE": "fixed-m1-signature",
            "GOPAY_D1": "fixed-d1",
            "GOPAY_SCREEN": "1080x2148",
            "GOPAY_DEVICE_OS": "Android, 99",
            "GOPAY_USER_AGENT": "fixed-user-agent",
            "GOPAY_WIFI_MAC": "02:00:00:00:00:00",
            "GOPAY_STATIC_DEVICE_IDENTITY": "",
        }, clear=False):
            first = gopay_client.generate_random_device_fingerprint()
            second = gopay_client.generate_random_device_fingerprint()

        for field, fixed in (
            ("x-uniqueid", "fixed-unique-id"),
            ("m1_appsflyer_id", "fixed-appsflyer-id"),
            ("m1_widevine_id", "fixed-widevine-id"),
            ("m1_signature", "fixed-m1-signature"),
            ("d1", "fixed-d1"),
            ("m1_wifi_mac", "02:00:00:00:00:00"),
        ):
            self.assertNotEqual(first[field], fixed)
            self.assertNotEqual(second[field], fixed)
            self.assertNotEqual(first[field], second[field])
        for device in (first, second):
            self.assertNotEqual(device["x-phonemodel"], "Google, sdk_gphone_arm64")
            self.assertNotEqual(device["m1_screen"], "1080x2148")
            self.assertNotEqual(device["x-deviceos"], "Android, 99")
            self.assertNotEqual(device["user-agent"], "fixed-user-agent")

    def test_ensure_device_defaults_randomizes_missing_template_fields(self):
        with patch.dict(gopay_client.os.environ, {
            "GOPAY_UNIQUE_ID": "fixed-unique-id",
            "GOPAY_SCREEN": "1080x2148",
            "GOPAY_STATIC_DEVICE_IDENTITY": "",
        }, clear=False):
            device = gopay_client._ensure_device_defaults({})

        self.assertNotEqual(device["x-uniqueid"], "fixed-unique-id")
        self.assertNotEqual(device["x-phonemodel"], "Google, sdk_gphone_arm64")
        self.assertNotEqual(device["m1_screen"], "1080x2148")

    def test_state_device_is_initialized_once_per_flow(self):
        state = {}
        saved = []

        with patch.object(gopay_app, "save_state", lambda target: saved.append(json.dumps(target, sort_keys=True))):
            first = gopay_app.ensure_state_device(state)
            first_snapshot = json.dumps(first, sort_keys=True)
            with patch.object(
                gopay_app,
                "generate_random_device_fingerprint",
                side_effect=AssertionError("device fingerprint should be reused"),
            ):
                second = gopay_app.ensure_state_device(state)

        self.assertIs(first, second)
        self.assertEqual(json.dumps(second, sort_keys=True), first_snapshot)
        self.assertEqual(state["device"]["profile_id"], first["profile_id"])
        self.assertEqual(len(saved), 1)

    def test_partial_state_device_is_completed_then_reused(self):
        state = {"device": {"x-uniqueid": "flow-device", "profile_id": "flow-profile"}}
        saved = []

        with patch.object(gopay_app, "save_state", lambda target: saved.append(json.dumps(target, sort_keys=True))):
            first = gopay_app.ensure_state_device(state)
            first_snapshot = json.dumps(first, sort_keys=True)
            second = gopay_app.ensure_state_device(state)

        self.assertIs(first, second)
        self.assertEqual(json.dumps(second, sort_keys=True), first_snapshot)
        self.assertEqual(first["x-uniqueid"], "flow-device")
        self.assertEqual(first["profile_id"], "flow-profile")
        self.assertTrue(first["profile_created_at"])
        self.assertEqual(len(saved), 1)

    def test_start_login_uses_fresh_profile_each_start(self):
        states = [{}, {}]
        profiles = [
            {"profile_id": "p1", "x-uniqueid": "u1"},
            {"profile_id": "p2", "x-uniqueid": "u2"},
        ]
        client_devices = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                client_devices.append(device)

            def post(self, url, body=None, **kwargs):
                return {
                    "status": 401,
                    "data": {},
                    "raw": {
                        "errors": [{
                            "message": f"Could not find the user {TEST_E164_PHONE}",
                            "message_title": "Invalid User",
                        }],
                    },
                }

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "new_logon_device_profile", side_effect=profiles), \
                patch.object(gopay_app, "GopayClient", FakeClient):
            first = gopay_app.start_login(states[0], TEST_LOCAL_PHONE, TEST_PIN, "+62")
            second = gopay_app.start_login(states[1], TEST_LOCAL_PHONE, TEST_PIN, "+62")

        self.assertTrue(first["not_registered"])
        self.assertTrue(second["not_registered"])
        self.assertEqual(states[0]["device"]["profile_id"], "p1")
        self.assertEqual(states[1]["device"]["profile_id"], "p2")
        self.assertEqual(client_devices[0]["profile_id"], "p1")
        self.assertEqual(client_devices[1]["profile_id"], "p2")

    def test_start_login_uses_nb_pin_endpoints_for_prelogin_pin(self):
        state = {}
        calls = []
        profile = {"profile_id": "login-profile", "x-uniqueid": "login-u1"}

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def post(self, url, body=None, **kwargs):
                calls.append(("post", url, body, kwargs))
                if url.endswith("/goto-auth/login/methods"):
                    return {
                        "status": 201,
                        "data": {
                            "methods": ["goto_pin"],
                            "verification_id": "login-verification-id",
                        },
                    }
                if url.endswith("/cvs/v1/initiate"):
                    return {"status": 200, "data": {"challenge_id": "challenge-id"}}
                if url.endswith("/pin/tokens/nb"):
                    return {"status": 200, "data": {"token": "validation-jwt"}}
                if url.endswith("/cvs/v1/verify"):
                    return {"status": 200, "data": {"verification_token": "verification-token"}}
                if url.endswith("/goto-auth/accountlist"):
                    return {
                        "status": 200,
                        "data": {
                            "account_list": [{"account_id": "account-id"}],
                            "1fa_token": "one-fa-token",
                        },
                    }
                if url.endswith("/goto-auth/token"):
                    return {
                        "status": 201,
                        "data": {
                            "access_token": jwt_with_exp(int(time.time()) + 3600),
                            "refresh_token": "refresh-token",
                            "expires_in": 1500,
                        },
                    }
                raise AssertionError(f"unexpected post {url}")

            def get(self, url, **kwargs):
                calls.append(("get", url, None, kwargs))
                if url.endswith("/pin-page/nb"):
                    return {"status": 200, "data": {}, "raw": {"success": True}}
                raise AssertionError(f"unexpected get {url}")

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "new_logon_device_profile", return_value=profile), \
                patch.object(gopay_app, "GopayClient", FakeClient):
            result = gopay_app.start_login(state, TEST_LOCAL_PHONE, TEST_PIN, "+62")

        self.assertTrue(result["success"])
        self.assertTrue(result["ready"])
        self.assertIn(
            ("get", "https://customer.gopayapi.com/api/v2/challenges/challenge-id/pin-page/nb", None, {}),
            calls,
        )
        self.assertIn(
            "https://customer.gopayapi.com/api/v1/users/pin/tokens/nb",
            [call[1] for call in calls],
        )


class ProfileHeaderTests(unittest.TestCase):
    def test_customer_slim_get_headers_match_gopay_capture_shape(self):
        device = gopay_app.new_logon_device_profile()
        client = gopay_client.GopayClient("access-token", device=device)

        with patch.object(gopay_client, "HMAC_KEY", "test-key"):
            header_sets = [
                client._headers("GET", url, "", None)
                for url in (
                    "https://customer.gopayapi.com/v1/users/profile",
                    "https://customer.gopayapi.com/v1/payment-options/balances",
                )
            ]

        for headers in header_sets:
            lower = {key.lower(): value for key, value in headers.items()}
            for key in (
                "authorization",
                "country-code",
                "gojek-service-area",
                "gojek-timezone",
                "x-appversion",
                "x-uniqueid",
                "x-phonemake",
                "x-help-version",
                "x-location",
                "x-location-accuracy",
                "x-e1",
                "x-deviceos",
                "x-user-type",
                "user-agent",
                "x-appid",
                "x-apptype",
                "x-e2",
                "x-m1",
                "x-phonemodel",
                "x-platform",
                "accept-language",
                "x-user-locale",
                "x-devicetoken",
                "x-authsdk-version",
                "x-cvsdk-version",
                "x-request-id",
                "transaction-id",
                "gojek-country-code",
            ):
                self.assertIn(key, lower)
            for key in (
                "x-e3",
                "d1",
                "x-session-id",
                "adjts",
                "x-pushtokentype",
                "user-uuid",
                "x-dark-mode",
                "content-type",
            ):
                self.assertNotIn(key, lower)

    def test_customer_pin_and_deactivation_headers_match_capture_shape(self):
        device = gopay_app.new_logon_device_profile()
        client = gopay_client.GopayClient("access-token", device=device)

        with patch.object(gopay_client, "HMAC_KEY", "test-key"):
            header_sets = [
                client._headers(
                    "POST",
                    "https://customer.gopayapi.com/api/v1/users/pin/challenges",
                    '{"flow":"pin_change"}',
                    None,
                ),
                client._headers(
                    "POST",
                    "https://customer.gopayapi.com/api/v1/users/pin/tokens",
                    '{"challenge_id":"challenge","client_id":"client","pin":"000000"}',
                    None,
                ),
                client._headers(
                    "POST",
                    "https://customer.gopayapi.com/api/v1/users/pin/tokens/nb",
                    '{"challenge_id":"challenge","client_id":"client","pin":"000000"}',
                    None,
                ),
                client._headers(
                    "GET",
                    "https://customer.gopayapi.com/api/v1/users/deactivate/check",
                    "",
                    None,
                ),
                client._headers(
                    "GET",
                    "https://customer.gopayapi.com/api/v2/challenges/challenge/pin-page/nb",
                    "",
                    None,
                ),
                client._headers(
                    "DELETE",
                    "https://customer.gopayapi.com/api/v1/users/deactivate",
                    '{"otp":"1234","reason":"I no longer need this account","description":null}',
                    None,
                ),
            ]

        for headers in header_sets:
            lower = {key.lower(): value for key, value in headers.items()}
            for key in (
                "accept-encoding",
                "authorization",
                "gojek-service-area",
                "country-code",
                "x-appversion",
                "x-m1",
                "gojek-country-code",
                "x-uniqueid",
                "x-phonemake",
                "x-help-version",
                "x-location",
                "x-location-accuracy",
                "x-e1",
                "x-deviceos",
                "x-user-type",
                "user-agent",
                "x-appid",
                "gojek-timezone",
                "x-apptype",
                "x-user-locale",
                "x-devicetoken",
                "x-e2",
                "x-authsdk-version",
                "x-cvsdk-version",
                "x-request-id",
                "transaction-id",
                "accept-language",
                "x-phonemodel",
                "x-platform",
            ):
                self.assertIn(key, lower)
            for key in (
                "x-e3",
                "d1",
                "x-session-id",
                "adjts",
                "x-pushtokentype",
                "user-uuid",
                "x-dark-mode",
            ):
                self.assertNotIn(key, lower)

        pin_headers = {key.lower(): value for key, value in header_sets[1].items()}
        self.assertEqual(pin_headers["sdk-version"], "2.7.0")
        self.assertEqual(pin_headers["x-biometric"], "")
        self.assertEqual(pin_headers["x-verification"], "PIN")

        nb_pin_headers = {key.lower(): value for key, value in header_sets[2].items()}
        for key in ("sdk-version", "x-biometric", "x-verification"):
            self.assertNotIn(key, nb_pin_headers)

    def test_gojek_activity_change_phone_headers_match_capture_shape(self):
        device = gopay_app.new_logon_device_profile()
        client = gopay_client.GopayClient("access-token", device=device)
        body = json.dumps({
            "email": "a@aa.cc",
            "name": "gg",
            "phone": "+6289600000000",
            "profile_image_url": None,
        }, separators=(",", ":"))

        with patch.object(gopay_client, "HMAC_KEY", "test-key"):
            headers = client._headers(
                "PATCH",
                "https://api.gojekapi.com/v5/customers",
                body,
                {"pin": "000000"},
            )

        lower = {key.lower(): value for key, value in headers.items()}
        for key in (
            "accept-encoding",
            "authorization",
            "gojek-service-area",
            "country-code",
            "x-appversion",
            "x-m1",
            "gojek-country-code",
            "x-uniqueid",
            "x-phonemake",
            "x-help-version",
            "x-location",
            "x-location-accuracy",
            "x-e1",
            "x-deviceos",
            "x-user-type",
            "user-agent",
            "x-appid",
            "gojek-timezone",
            "content-type",
            "x-apptype",
            "x-user-locale",
            "x-devicetoken",
            "x-e2",
            "x-authsdk-version",
            "x-cvsdk-version",
            "x-request-id",
            "transaction-id",
            "accept-language",
            "x-phonemodel",
            "x-platform",
            "pin",
        ):
            self.assertIn(key, lower)
        for key in (
            "x-e3",
            "d1",
            "x-session-id",
            "adjts",
            "x-pushtokentype",
            "user-uuid",
            "x-dark-mode",
        ):
            self.assertNotIn(key, lower)

    def test_linkedapps_and_unlink_headers_match_capture_shape(self):
        device = gopay_app.new_logon_device_profile()
        client = gopay_client.GopayClient("access-token", device=device)

        with patch.object(gopay_client, "HMAC_KEY", "test-key"):
            header_sets = [
                client._headers("GET", "https://customer.gopayapi.com/v1/linkedapps", "", None),
                client._headers("PATCH", "https://customer.gopayapi.com/v1/links/link-1", "", None),
                client._headers(
                    "GET",
                    "https://customer.gopayapi.com/v1/festivals/envelope-requests/test-envelope-request-id",
                    "",
                    None,
                ),
                client._headers("GET", "https://api.gojekapi.com/courier/v1/token", "", None),
                client._headers("GET", "https://api.gojekapi.com/gojek/v2/customer", "", None),
            ]

        for headers in header_sets:
            lower = {key.lower(): value for key, value in headers.items()}
            for key in (
                "accept-encoding",
                "authorization",
                "gojek-service-area",
                "country-code",
                "x-appversion",
                "x-m1",
                "gojek-country-code",
                "x-uniqueid",
                "x-phonemake",
                "x-help-version",
                "x-location",
                "x-location-accuracy",
                "x-e1",
                "x-deviceos",
                "x-user-type",
                "user-agent",
                "x-appid",
                "gojek-timezone",
                "x-apptype",
                "x-user-locale",
                "x-devicetoken",
                "x-e2",
                "x-authsdk-version",
                "x-cvsdk-version",
                "x-request-id",
                "transaction-id",
                "accept-language",
                "x-phonemodel",
                "x-platform",
            ):
                self.assertIn(key, lower)
            for key in (
                "x-e3",
                "d1",
                "x-session-id",
                "adjts",
                "x-pushtokentype",
                "user-uuid",
                "x-dark-mode",
                "content-type",
            ):
                self.assertNotIn(key, lower)


class ReplayTests(unittest.TestCase):
    def test_build_midtrans_link_order_from_deeplink(self):
        payment_ref = "A220260514182119XINUsw6rOZID"

        order = replay.build_midtrans_link_order(f"gopay://pay?reference={payment_ref}")
        qris_body = replay.build_midtrans_link_qris_body(f"gopay://pay?reference={payment_ref}")

        self.assertEqual(order["payment_id"], payment_ref)
        self.assertEqual(order["amount"], {"value": 1, "currency": "IDR"})
        self.assertEqual(order["channel_type"], "ONLINE_GATEWAY")
        info = order["additional_data"]["aspiqr_information"]
        self.assertEqual(info["merchant_id"], "G761482587")
        self.assertEqual(
            info["additional_data_national"],
            replay.build_additional_data_national("12190", {"07": "A01", "50": payment_ref}),
        )
        self.assertIn(payment_ref, order["metadata"]["aspi_qr_data"])
        self.assertEqual(qris_body["channel_type"], "DYNAMIC_QR")
        self.assertIn(payment_ref, qris_body["qr_code"])
        self.assertEqual(
            qris_body["qr_code"],
            "00020101021226610014COM.GO-JEK.WWW01189360091437614825870210G761482587"
            "0303UBE51440014ID.CO.QRIS.WWW0215ID20254554280810303UBE520458175303360"
            "540115802ID5910OpenAI LLC6015JAKARTA SELATAN61051219062395028"
            "A220260514182119XINUsw6rOZID0703A016304244F",
        )
        self.assertEqual(qris_body["qr_code"][-8:-4], "6304")
        expected_crc = replay.crc16_ccitt(qris_body["qr_code"][:-4].encode("ascii"))
        self.assertEqual(qris_body["qr_code"][-4:], f"{expected_crc:04X}")

    def test_run_link_payment_uses_payment_ref_and_pin_without_files(self):
        calls = []
        case = self
        payment_ref = "A220260514182119XINUsw6rOZID"
        original_payment_option_id = "00000000-0000-4000-8000-000000000001"
        randomized_payment_option_id = "11111111-1111-4111-8111-111111111111"
        wallet_token = base64.b64encode(json.dumps({
            "type": "GOPAY_WALLET",
            "intentString": "GOPAY_WALLET",
            "payment_option_id": original_payment_option_id,
        }, separators=(",", ":")).encode()).decode()
        randomized_token = {"value": ""}

        def assert_randomized_payment_token(token: str):
            case.assertNotEqual(token, wallet_token)
            payload = replay.decode_payment_option_token(token)
            case.assertEqual(payload["type"], "GOPAY_WALLET")
            case.assertEqual(payload["intentString"], "GOPAY_WALLET")
            case.assertEqual(payload["payment_option_id"], randomized_payment_option_id)
            if randomized_token["value"]:
                case.assertEqual(token, randomized_token["value"])
            else:
                randomized_token["value"] = token

        class FakeClient:
            def post(self, url, body=None, extra_headers=None, **kwargs):
                calls.append(("post", url, body, extra_headers or {}))
                if url.endswith("/checkout/list"):
                    case.assertEqual(body["merchant_id"], "G761482587")
                    case.assertEqual(body["service_id"], "1002")
                    case.assertEqual(body["metadata"], {"merchant_id": "G761482587"})
                    return {
                        "status": 200,
                        "data": {"selected_options": [{"token": wallet_token}]},
                        "raw": {"success": True},
                    }
                if url.endswith("/promotions/evaluate"):
                    case.assertEqual(body["payment_instructions"][0]["token"], wallet_token)
                    case.assertEqual(body, {
                        "order_id": payment_ref,
                        "payment_instructions": [{
                            "token": wallet_token,
                            "amount": {"value": 1, "currency": "IDR"},
                        }],
                        "transaction_type": "MERCHANT_TRANSACTION",
                    })
                    return {"status": 200, "data": {}, "raw": {"success": True}}
                if url.endswith("/pin/tokens"):
                    case.assertEqual(body, {
                        "pin": TEST_PIN,
                        "client_id": "client-id",
                        "challenge_id": "challenge-id",
                    })
                    return {
                        "status": 200,
                        "data": {"token": "pin-token-from-server"},
                        "raw": {"success": True},
                    }
                raise AssertionError(f"unexpected post {url}")

            def put(self, url, body=None, **kwargs):
                calls.append(("put", url, body, {}))
                case.assertEqual(body, {"token": wallet_token})
                return {"status": 200, "data": {}, "raw": {"success": True}}

            def get(self, url, **kwargs):
                calls.append(("get", url, None, {}))
                if "/customers/v1/payments/" in url:
                    case.assertIn(payment_ref, url)
                    return {
                        "status": 200,
                        "data": {
                            "payment_id": payment_ref,
                            "payment_intent": "EWALLET_QR",
                            "amount": {"value": 1, "currency": "IDR"},
                            "merchant_information": {"merchant_id": "G761482587"},
                        },
                        "raw": {"success": True},
                    }
                case.assertIn("/pin-page", url)
                return {"status": 200, "data": {}, "raw": {"success": True}}

            def patch(self, url, body=None, extra_headers=None, **kwargs):
                calls.append(("patch", url, body, extra_headers or {}))
                case.assertIn(payment_ref, url)
                assert_randomized_payment_token(body["payment_instructions"][0]["token"])
                case.assertIsNone(body["channel_type"])
                case.assertIsNone(body["additional_data"])
                case.assertIsNone(body["metadata"])
                case.assertIsNone(body["checksum"])
                case.assertIsNone(body["order_signature"])
                if body.get("challenge") is None:
                    return {
                        "status": 461,
                        "data": {
                            "challenge": {
                                "action": {"value": {"challenge_id": "challenge-id", "client_id": "client-id"}},
                                "metadata": {"challenge_id": "challenge-id", "client_id": "client-id"},
                            }
                        },
                        "raw": {"success": False},
                    }
                case.assertEqual(body["challenge"]["value"]["pin_token"], "pin-token-from-server")
                return {"status": 200, "data": {"payment_id": "pay-1", "status": "PAID"}, "raw": {"success": True}}

        with patch.object(replay.uuid, "uuid4", return_value=randomized_payment_option_id):
            result = replay.run_link_payment(
                FakeClient(),
                replay.LinkPaymentOptions(payment_link=f"gopay://pay?reference={payment_ref}", pin=TEST_PIN),
            )

        self.assertTrue(result["success"], result.get("error_message"))
        self.assertEqual(result["payment_id"], payment_ref)
        self.assertEqual([call[0] for call in calls], ["get", "post", "post", "patch", "put", "get", "post", "patch"])
        self.assertEqual([step["label"] for step in result["steps"]], [
            "payment_detail", "checkout_list", "promotions_evaluate", "capture1",
            "last_used", "pin_page", "pin_tokens", "capture2",
        ])

    def test_run_linked_app_unlink_patches_empty_body(self):
        calls = []

        class FakeClient:
            def get(self, url, **kwargs):
                calls.append(("get", url, None))
                return {
                    "status": 200,
                    "data": {
                        "linked_services": [{
                            "service_name": "OpenAI LLC",
                            "unlink_service_url": "/v1/links/link-1",
                        }]
                    },
                    "raw": {"success": True},
                }

            def patch(self, url, body=None, **kwargs):
                calls.append(("patch", url, body))
                return {"status": 202, "data": {}, "raw": {"success": True}}

        result = replay.run_linked_app_unlink(FakeClient())

        self.assertTrue(result["success"], result.get("error_message"))
        self.assertEqual(result["unlinked_count"], 1)
        self.assertEqual(calls, [
            ("get", "https://customer.gopayapi.com/v1/linkedapps", None),
            ("patch", "https://customer.gopayapi.com/v1/links/link-1", None),
        ])
        self.assertEqual([step["label"] for step in result["steps"]], ["linkedapps", "unlink:OpenAI LLC"])

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_replay_link_rpc_uses_state_token(self):
        returned = {
            "success": True,
            "error_message": "",
            "payment_id": "pay-1",
            "status": "PAID",
            "steps": [{"label": "capture2", "status_code": 200}],
        }
        state = {"stage": "ready", "token": "state-token", "device": {}}
        fake_client = object()
        with patch.object(app_server, "_client", return_value=fake_client) as make_client, \
                patch.object(app_server, "run_link_payment", return_value=returned) as run:
            resp = app_server.GopayAppServicer().ReplayLinkPayment(
                app_server.gopay_app_pb2.ReplayLinkPaymentRequest(
                    payment_link="gopay://pay?reference=A220260514182119XINUsw6rOZID",
                    pin=TEST_PIN,
                    state_json=json.dumps(state),
                ),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(resp.payment_id, "pay-1")
        self.assertEqual(json.loads(resp.state_json)["token"], "state-token")
        make_client.assert_called_once()
        self.assertIs(run.call_args.args[0], fake_client)


class LoginStartTests(unittest.TestCase):
    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_consumed_stage_uses_valid_token_without_otp(self):
        state = {
            "stage": "consumed",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "refresh_token": "refresh-token",
        }

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        def fake_check_token_valid(target):
            target["stage"] = "ready"
            state["stage"] = "ready"
            return {
                "success": True,
                "token_valid": True,
                "has_min_balance": True,
                "balance_amount": 1,
            }

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "check_token_valid", fake_check_token_valid), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().LoginStart(
                app_server.gopay_app_pb2.LoginStartRequest(),
                None,
            )

        self.assertTrue(resp.success)
        self.assertFalse(resp.otp_sent)
        self.assertEqual(state["stage"], "ready")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_login_start_returns_unregistered_without_signup(self):
        state = {"stage": "idle"}
        captured = {}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        def fake_start_login(target, phone, pin, country_code, otp_channel=""):
            captured["phone"] = phone
            captured["pin"] = pin
            captured["country_code"] = country_code
            return {"success": False, "not_registered": True, "error": "invalid user"}

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "start_login", fake_start_login), \
                patch.object(app_server.GopayAppServicer, "SignupStart", side_effect=AssertionError("signup must not be called")):
            resp = app_server.GopayAppServicer().LoginStart(
                app_server.gopay_app_pb2.LoginStartRequest(
                    phone=TEST_CHANGE_FULL_PHONE,
                    country_code="+62",
                    pin=TEST_PIN,
                ),
                None,
            )

        self.assertFalse(resp.success)
        self.assertEqual(resp.error_message, "账户未注册")
        self.assertEqual(captured["phone"], TEST_CHANGE_LOCAL_PHONE)
        self.assertEqual(captured["pin"], TEST_PIN)
        self.assertEqual(captured["country_code"], "+62")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_status_expires_stale_login_otp_pending(self):
        state = {
            "stage": "login_otp_pending",
            "_login_phone": TEST_LOCAL_PHONE,
            "_login_otp_token": "otp-token",
            "_login_2fa_token": "2fa-token",
        }

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state):
            resp = app_server.GopayAppServicer().Status(
                app_server.gopay_app_pb2.StatusRequest(),
                None,
            )

        self.assertEqual(resp.stage, "idle")
        self.assertFalse(resp.token_present)
        self.assertEqual(resp.error_message, "LOGIN_OTP_TIMEOUT")
        self.assertNotIn("_login_otp_token", state)

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_auth_start_returns_ready_when_token_valid(self):
        state = {"stage": "ready", "token": "access-token"}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "check_token_valid", lambda target: {"success": True, "token_valid": True, "has_min_balance": True, "balance_amount": 1}), \
                patch.object(app_server, "start_login") as start_login:
            resp = app_server.GopayAppServicer().AuthStart(
                app_server.gopay_app_pb2.AuthStartRequest(),
                None,
            )

        self.assertTrue(resp.success)
        self.assertTrue(resp.ready)
        self.assertEqual(resp.mode, "token")
        start_login.assert_not_called()

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_auth_start_accepts_valid_token_without_min_balance(self):
        state = {"stage": "ready", "token": "access-token", "balance_amount": 0, "balance_currency": "IDR"}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "check_token_valid", lambda target: {
                    "success": True,
                    "token_valid": True,
                    "has_min_balance": False,
                    "balance_amount": 0,
                    "balance_currency": "IDR",
                }), \
                patch.object(app_server, "start_login") as start_login:
            resp = app_server.GopayAppServicer().AuthStart(
                app_server.gopay_app_pb2.AuthStartRequest(),
                None,
            )

        self.assertTrue(resp.success)
        self.assertTrue(resp.ready)
        self.assertEqual(resp.mode, "token")
        self.assertEqual(resp.error_message, "")
        start_login.assert_not_called()

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_auth_start_requires_upstream_phone_for_login_probe(self):
        state = {"stage": "idle", "phone": TEST_CHANGE_LOCAL_PHONE}
        captured = {}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        def fake_start_login(target, phone, pin, country_code, otp_channel=""):
            captured["phone"] = phone
            captured["country_code"] = country_code
            return {
                "success": True,
                "ready": False,
                "otp_sent": True,
                "verification_id": "verification-id",
                "method": "otp_wa",
            }

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "check_token_valid", lambda target: {"success": False, "token_valid": False}), \
                patch.object(app_server, "start_login", fake_start_login):
            resp = app_server.GopayAppServicer().AuthStart(
                app_server.gopay_app_pb2.AuthStartRequest(phone=TEST_CHANGE_LOCAL_PHONE),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(resp.mode, "login")
        self.assertEqual(captured["phone"], TEST_CHANGE_LOCAL_PHONE)
        self.assertEqual(captured["country_code"], "+62")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_signup_start_requires_upstream_phone(self):
        state = {"stage": "idle", "phone": TEST_CHANGE_LOCAL_PHONE}
        captured = {}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        def fake_start_signup(target, phone, name, email, country_code, otp_channel=""):
            captured["phone"] = phone
            captured["name"] = name
            captured["email"] = email
            captured["country_code"] = country_code
            return {
                "success": True,
                "otp_sent": True,
                "verification_id": "verification-id",
                "method": "otp_wa",
            }

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "start_signup", fake_start_signup):
            resp = app_server.GopayAppServicer().SignupStart(
                app_server.gopay_app_pb2.SignupStartRequest(phone=TEST_CHANGE_LOCAL_PHONE),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(captured["phone"], TEST_CHANGE_LOCAL_PHONE)
        self.assertEqual(captured["country_code"], "+62")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_signup_start_generates_profile_when_name_missing_and_keeps_empty_email(self):
        state = {"stage": "idle"}
        captured = {}

        def fake_start_signup(target, phone, name, email, country_code, otp_channel=""):
            captured["phone"] = phone
            captured["name"] = name
            captured["email"] = email
            captured["country_code"] = country_code
            return {
                "success": True,
                "otp_sent": True,
                "verification_id": "verification-id",
                "method": "otp_sms",
            }

        with patch.object(app_server, "load_state", lambda: dict(state)), \
                patch.object(app_server, "save_state", lambda target: None), \
                patch.object(app_server, "start_signup", fake_start_signup), \
                patch.object(app_server, "GOPAY_SIGNUP_NAME", ""), \
                patch.object(app_server, "GOPAY_SIGNUP_EMAIL", ""), \
                patch.object(app_server.time, "time", return_value=1770000000), \
                patch.object(app_server.os, "urandom", return_value=bytes.fromhex("abcdef")):
            resp = app_server.GopayAppServicer().SignupStart(
                app_server.gopay_app_pb2.SignupStartRequest(phone=TEST_CHANGE_LOCAL_PHONE),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(captured["name"], "op")
        self.assertEqual(captured["email"], "")


class SignupFlowTests(unittest.TestCase):
    def test_otp_method_selection_distinguishes_sms_and_wa(self):
        methods = ["otp_wa", "otp_sms"]

        self.assertEqual(gopay_app._choose_method(methods, ""), "otp_sms")
        self.assertEqual(gopay_app._choose_method(methods, "sms"), "otp_sms")
        self.assertEqual(gopay_app._choose_method(methods, "wa"), "otp_wa")
        self.assertEqual(gopay_app._choose_method(["otp_wa"], "sms"), "")

    def test_signup_basic_authorization_uses_fixed_uuid(self):
        expected = "Basic " + base64.b64encode(
            gopay_app.GOPAY_SIGNUP_AUTH_UUID.encode("utf-8")
        ).decode("ascii")

        self.assertEqual(gopay_app._signup_basic_authorization(), expected)

    def test_start_signup_uses_signup_cvs_flow(self):
        state = {}
        calls = []
        client_devices = []
        profile = {"profile_id": "signup-profile", "x-uniqueid": "signup-u1"}

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token
                client_devices.append(device)

            def post(self, url, body=None, **kwargs):
                calls.append((url, body, kwargs))
                if url.endswith("/cvs/v1/methods"):
                    return {
                        "status": 200,
                        "data": {
                            "default_method": "otp_wa",
                            "methods": ["otp_wa", "otp_sms"],
                            "verification_id": "verification-id",
                        },
                    }
                if url.endswith("/cvs/v1/initiate"):
                    return {"status": 200, "data": {"otp_token": "signup-otp-token"}}
                raise AssertionError(f"unexpected post {url}")

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "new_logon_device_profile", return_value=profile), \
                patch.object(gopay_app, "GopayClient", FakeClient):
            result = gopay_app.start_signup(
                state,
                phone=TEST_FULL_PHONE,
                name="gg",
                email="gg@example.test",
                country_code="+62",
            )

        self.assertTrue(result["success"])
        self.assertEqual(state["stage"], "signup_otp_pending")
        self.assertEqual(state["_signup_phone"], TEST_LOCAL_PHONE)
        self.assertEqual(state["_signup_otp_token"], "signup-otp-token")
        self.assertEqual(state["device"]["profile_id"], "signup-profile")
        self.assertEqual(client_devices[0]["profile_id"], "signup-profile")
        self.assertEqual(calls[0][1]["flow"], "signup")
        self.assertEqual(calls[0][1]["country_code"], "+62")
        self.assertEqual(calls[1][1]["verification_method"], "otp_sms")

    def test_retry_signup_otp_uses_cvs_retry_flow(self):
        state = {
            "stage": "signup_otp_pending",
            "token": "access-token",
            "device": {"profile_id": "signup-profile"},
            "_signup_verification_method": "otp_sms",
            "_signup_otp_token": "signup-otp-token",
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token
                self.device = device

            def post(self, url, body=None, **kwargs):
                calls.append((url, body, kwargs, self.token, self.device))
                if url.endswith("/cvs/v1/retry"):
                    return {
                        "status": 200,
                        "success": True,
                        "data": {"otp_token": "retry-otp-token"},
                    }
                raise AssertionError(f"unexpected post {url}")

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app.time, "time", return_value=1770000123), \
                patch.object(gopay_app, "GopayClient", FakeClient):
            result = gopay_app.retry_signup_otp(state)

        self.assertTrue(result["success"])
        self.assertEqual(calls[0][0], "https://accounts.goto-products.com/cvs/v1/retry")
        self.assertEqual(calls[0][1]["flow"], "signup")
        self.assertEqual(calls[0][1]["verification_method"], "otp_sms")
        self.assertEqual(calls[0][1]["data"]["otp_token"], "signup-otp-token")
        self.assertEqual(calls[0][3], "access-token")
        self.assertEqual(calls[0][4]["profile_id"], "signup-profile")
        self.assertEqual(state["_signup_otp_token"], "retry-otp-token")
        self.assertEqual(state["_signup_otp_sent_at"], 1770000123)
        self.assertIn('"success":true', result["raw_json"])

    def test_complete_signup_creates_customer_and_refreshes_token(self):
        state = {
            "stage": "signup_otp_pending",
            "token": "old-token",
            "device": {},
            "_signup_phone": TEST_LOCAL_PHONE,
            "_signup_country_code": "+62",
            "_signup_name": "gg",
            "_signup_email": "",
            "_signup_verification_id": "verification-id",
            "_signup_verification_method": "otp_wa",
            "_signup_otp_token": "signup-otp-token",
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def post(self, url, body=None, **kwargs):
                calls.append((url, body, kwargs))
                if url.endswith("/cvs/v1/verify"):
                    return {"status": 200, "data": {"verification_token": "verification-token"}}
                if url.endswith("/v7/customers/signup"):
                    return {
                        "status": 201,
                        "data": {
                            "access_token": jwt_with_exp(int(time.time()) + 60),
                            "refresh_token": "refresh-token",
                            "expires_in": 1500,
                        },
                    }
                raise AssertionError(f"unexpected post {url}")

        def fake_refresh(target, min_ttl_seconds=None, force=False):
            target["token"] = jwt_with_exp(int(time.time()) + 3600)
            return {"success": True, "refreshed": True}

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "ensure_access_token", fake_refresh):
            result = gopay_app.complete_signup(state, "1234")

        self.assertTrue(result["success"])
        self.assertEqual(state["stage"], "signup_pin_required")
        self.assertEqual(state["phone"], TEST_LOCAL_PHONE)
        self.assertNotIn("_signup_otp_token", state)
        signup_call = calls[1]
        self.assertEqual(signup_call[1]["data"]["phone"], TEST_E164_PHONE)
        self.assertEqual(signup_call[1]["data"]["email"], "")
        self.assertEqual(
            signup_call[2]["extra_headers"],
            {
                "Authorization": gopay_app._signup_basic_authorization(),
                "Verification-Token": "Bearer verification-token",
            },
        )

    def test_signup_pin_start_and_complete_use_pin_setup_flow(self):
        state = {
            "stage": "signup_pin_required",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "phone": TEST_LOCAL_PHONE,
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                if url.endswith("/v1/users/profile"):
                    return {"status": 200, "data": {"is_pin_setup": False}}
                raise AssertionError(f"unexpected get {url}")

            def post(self, url, body=None, **kwargs):
                calls.append((url, body, kwargs))
                if url.endswith("/api/v1/users/pins/allowed"):
                    return {"status": 200, "data": {"success": True}}
                if url.endswith("/cvs/v1/methods"):
                    return {
                        "status": 200,
                        "data": {
                            "default_method": "otp_wa",
                            "methods": ["otp_wa", "otp_sms"],
                            "verification_id": "pin-verification-id",
                        },
                    }
                if url.endswith("/cvs/v1/initiate"):
                    return {"status": 200, "data": {"otp_token": "pin-otp-token"}}
                if url.endswith("/cvs/v1/verify"):
                    return {"status": 200, "data": {"verification_token": "pin-verification-token"}}
                if url.endswith("/api/v2/users/pins/setup/tokens"):
                    return {"status": 200, "data": {"token": "pin-token"}}
                raise AssertionError(f"unexpected post {url}")

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "ensure_access_token", lambda target, **kwargs: {"success": True}):
            start = gopay_app.start_signup_pin(state, TEST_PIN)
            complete = gopay_app.complete_signup_pin(state, "1234", TEST_PIN)

        self.assertTrue(start["success"])
        self.assertTrue(complete["success"])
        self.assertEqual(state["stage"], "ready")
        self.assertEqual(state["phone"], TEST_LOCAL_PHONE)
        self.assertNotIn("_signup_pin_otp_token", state)
        methods_call = calls[1]
        self.assertEqual(methods_call[1]["flow"], "goto_pin_wa_sms")
        self.assertIsNone(methods_call[1]["country_code"])
        self.assertIsNone(methods_call[1]["phone_number"])
        initiate_call = calls[2]
        self.assertEqual(initiate_call[1]["flow"], "goto_pin_wa_sms")
        self.assertEqual(initiate_call[1]["verification_method"], "otp_sms")
        self.assertIsNone(initiate_call[1]["phone_number"])
        verify_call = calls[3]
        self.assertEqual(verify_call[1]["flow"], "goto_pin_wa_sms")
        self.assertEqual(verify_call[1]["verification_method"], "otp_sms")
        setup_call = calls[-1]
        self.assertEqual(setup_call[1], {
            "client_id": "",
            "pin": TEST_PIN,
            "challenge_id": "",
        })
        self.assertEqual(
            setup_call[2]["extra_headers"],
            {
                "Verification-Token": "Bearer pin-verification-token",
                "Is-Token-Required": "false",
            },
        )

    def test_signup_pin_start_treats_existing_pin_as_settled(self):
        state = {
            "stage": "ready",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "phone": TEST_LOCAL_PHONE,
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                calls.append(("get", url))
                if url.endswith("/v1/users/profile"):
                    return {"status": 200, "data": {"is_pin_setup": True, "phone": TEST_E164_PHONE}}
                raise AssertionError(f"unexpected get {url}")

            def post(self, url, body=None, **kwargs):
                raise AssertionError(f"pin setup endpoint should not be called: {url}")

        with patch.object(gopay_app, "save_state", lambda target: None), \
                patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app, "ensure_access_token", lambda target, **kwargs: {"success": True}):
            result = gopay_app.start_signup_pin(state, TEST_PIN)

        self.assertTrue(result["success"])
        self.assertTrue(result["pin_setup_complete"])
        self.assertEqual(state["stage"], "ready")
        self.assertEqual(state["phone"], TEST_LOCAL_PHONE)
        self.assertEqual(calls, [("get", "https://customer.gopayapi.com/v1/users/profile")])


class EnvelopeFlowTests(unittest.TestCase):
    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_extracts_envelope_id_from_shortlink_html(self):
        envelope_id = "test-envelope-request-id"
        html = (
            "<script>var app_link = "
            f"'gopay://envelope/home?data=%7Benvelope_request_id:{envelope_id}%7D';</script>"
        )

        self.assertEqual(app_server._extract_envelope_request_id(html), envelope_id)

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_claim_envelope_uses_festivals_endpoint_and_loads_detail(self):
        calls = []

        class FakeClient:
            def post(self, url, body=None, **kwargs):
                calls.append(("post", url, body))
                return {
                    "status": 200,
                    "data": {"envelope_request_id": "response-envelope-id"},
                    "raw": {"data": {"envelope_request_id": "response-envelope-id"}, "success": True},
                }

            def get(self, url, **kwargs):
                calls.append(("get", url, None))
                return {
                    "status": 200,
                    "data": {
                        "envelope_request_id": "response-envelope-id",
                        "status": "OPENED",
                        "amount": 20,
                        "currency": "IDR",
                    },
                    "raw": {
                        "data": {
                            "envelope_request_id": "response-envelope-id",
                            "status": "OPENED",
                            "amount": 20,
                            "currency": "IDR",
                        },
                        "success": True,
                    },
                }

        response = app_server._claim_envelope(FakeClient(), "request-envelope-id")

        self.assertEqual(response["status"], 200)
        self.assertEqual(response["data"]["status"], "OPENED")
        self.assertEqual(calls[0], (
            "post",
            "https://customer.gopayapi.com/v1/festivals/envelope-requests",
            {"envelope_request_id": "request-envelope-id"},
        ))
        self.assertEqual(calls[1], (
            "get",
            "https://customer.gopayapi.com/v1/festivals/envelope-requests/request-envelope-id",
            None,
        ))

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_claim_envelope_posts_claim_endpoint(self):
        envelope_id = "test-envelope-request-id"
        state = {
            "stage": "ready",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "device": {},
        }
        client = object()
        calls = []

        def fake_claim(target_client, target_envelope_id):
            calls.append((target_client, target_envelope_id))
            return {
                "status": 200,
                "data": {
                    "amount": 2,
                    "currency": "IDR",
                    "remaining": 88,
                    "total": 100,
                },
                "raw": {
                    "amount": 2,
                    "currency": "IDR",
                    "remaining": 88,
                    "total": 100,
                },
            }

        with patch.object(app_server, "_client", lambda target: client), \
                patch.object(app_server, "_claim_envelope", fake_claim):
            resp = app_server.GopayAppServicer().ClaimEnvelope(
                app_server.gopay_app_pb2.ClaimEnvelopeRequest(
                    envelope_request_id=envelope_id,
                    state_json=json.dumps(state),
                ),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(resp.envelope_request_id, envelope_id)
        self.assertEqual(resp.http_status, 200)
        self.assertIn('"amount":2', resp.raw_json)
        self.assertEqual(calls, [(client, envelope_id)])


class DeactivationFlowTests(unittest.TestCase):
    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_deactivate_start_skips_pin_for_account_without_pin_and_accepts_otp_required(self):
        state = {
            "_tmp_token": jwt_with_exp(int(time.time()) + 3600),
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                calls.append(("get", url))
                if url.endswith("/v1/users/profile"):
                    return {"status": 200, "data": {"is_pin_setup": False}}
                if url.endswith("/api/v1/users/deactivate/check"):
                    return {
                        "status": 462,
                        "data": {},
                        "raw": {"errors": [{"code": "GoPay-1603", "message": "You must enter OTP to continue"}]},
                    }
                raise AssertionError(f"unexpected get {url}")

            def post(self, url, body=None, **kwargs):
                raise AssertionError(f"pin endpoint should not be called: {url}")

        with patch.object(app_server, "load_state", lambda: state), \
                patch.object(app_server, "save_state", lambda target: None), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "GOPAY_PIN", ""):
            resp = app_server.GopayAppServicer().DeactivateStart(
                app_server.gopay_app_pb2.DeactivateStartRequest(),
                None,
            )

        self.assertTrue(resp.success)
        self.assertTrue(resp.otp_sent)
        self.assertEqual(state["stage"], "deactivate_otp_pending")
        self.assertEqual(calls, [
            ("get", "https://customer.gopayapi.com/v1/users/profile"),
            ("get", "https://customer.gopayapi.com/api/v1/users/deactivate/check"),
        ])

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_deactivate_complete_uses_captured_reason_and_clears_tmp_token(self):
        state = {
            "stage": "deactivate_otp_pending",
            "_tmp_token": jwt_with_exp(int(time.time()) + 3600),
            "_tmp_refresh_token": "refresh-token",
            "device": {},
        }
        bodies = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def delete(self, url, body=None, **kwargs):
                bodies.append((url, body))
                return {"status": 200, "data": {"success": True}}

        with patch.object(app_server, "load_state", lambda: state), \
                patch.object(app_server, "save_state", lambda target: None), \
                patch.object(app_server, "GopayClient", FakeClient):
            resp = app_server.GopayAppServicer().DeactivateComplete(
                app_server.gopay_app_pb2.DeactivateCompleteRequest(otp="1234"),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(state["stage"], "deactivated")
        self.assertNotIn("_tmp_token", state)
        self.assertEqual(bodies, [(
            "https://customer.gopayapi.com/api/v1/users/deactivate",
            {
                "otp": "1234",
                "reason": "I no longer need digital payment services",
                "description": None,
            },
        )])


class ChangePhoneActivityFlowTests(unittest.TestCase):
    def test_check_phone_login_methods_uses_fresh_fingerprint_per_call(self):
        devices = [{"profile_id": "p1"}, {"profile_id": "p2"}]
        used_devices = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                used_devices.append(device)

            def post(self, url, body=None, **kwargs):
                return {
                    "status": 401,
                    "raw": {
                        "errors": [{
                            "message_title": "Invalid User",
                            "message": "Could not find the user +628000000000",
                        }]
                    },
                }

        with patch.dict(gopay_app.os.environ, {"GOPAY_PROXY_POOL": "http://proxy-1 http://proxy-2"}), \
                patch.object(gopay_app, "new_logon_device_profile", side_effect=devices), \
                patch.object(gopay_app, "GopayClient", FakeClient):
            first = gopay_app.check_phone_by_login_methods("8000000000", "+62")
            second = gopay_app.check_phone_by_login_methods("8000000001", "+62")

        self.assertTrue(first["available"])
        self.assertTrue(second["available"])
        self.assertEqual(used_devices, devices)

    def test_check_phone_login_methods_rotates_fingerprint_and_proxy_after_rate_limit(self):
        devices = [{"profile_id": "p1"}, {"profile_id": "p2"}, {"profile_id": "p3"}]
        used_devices = []
        used_proxies = []
        responses = [
            {"status": 429, "raw": {"errors": [{"code": "RateLimited"}]}},
            {"status": 429, "raw": {"errors": [{"code": "RateLimited"}]}},
            {
                "status": 401,
                "raw": {
                    "errors": [{
                        "message_title": "Invalid User",
                        "message": "Could not find the user +628000000000",
                    }]
                },
            },
        ]

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                used_devices.append(device)
                used_proxies.append(proxy)

            def post(self, url, body=None, **kwargs):
                return responses.pop(0)

        with patch.dict(gopay_app.os.environ, {"GOPAY_PROXY_POOL": "http://proxy-1 http://proxy-2,http://proxy-3"}), \
                patch.object(gopay_app, "new_logon_device_profile", side_effect=devices), \
                patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app.time, "sleep", lambda _seconds: None):
            result = gopay_app.check_phone_by_login_methods("8000000000", "+62")

        self.assertTrue(result["available"])
        self.assertEqual(result["attempts"], 3)
        self.assertEqual(result["fingerprint_rotations"], 2)
        self.assertEqual(result["proxy_rotations"], 2)
        self.assertEqual(result["proxy_pool_size"], 3)
        self.assertEqual(used_devices, devices)
        self.assertEqual(used_proxies, ["http://proxy-1", "http://proxy-2", "http://proxy-3"])

    def test_check_phone_login_methods_fails_when_proxy_wheel_returns_to_first(self):
        devices = [{"profile_id": "p1"}, {"profile_id": "p2"}]
        used_devices = []
        used_proxies = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                used_devices.append(device)
                used_proxies.append(proxy)

            def post(self, url, body=None, **kwargs):
                return {"status": 429, "raw": {"errors": [{"code": "RateLimited"}]}}

        with patch.dict(gopay_app.os.environ, {"GOPAY_PROXY_POOL": "http://proxy-1,http://proxy-2"}), \
                patch.object(gopay_app, "new_logon_device_profile", side_effect=devices), \
                patch.object(gopay_app, "GopayClient", FakeClient), \
                patch.object(gopay_app.time, "sleep", lambda _seconds: None):
            result = gopay_app.check_phone_by_login_methods("8000000000", "+62")

        self.assertFalse(result["success"])
        self.assertEqual(result["status"], "rate_limited")
        self.assertIn("GOPAY_PROXY_POOL exhausted", result["error"])
        self.assertEqual(result["attempts"], 2)
        self.assertEqual(result["proxy_pool_size"], 2)
        self.assertEqual(used_devices, devices)
        self.assertEqual(used_proxies, ["http://proxy-1", "http://proxy-2"])

    def test_proxy_rotation_starts_from_previous_proxy_without_first_state(self):
        state = {"_gopay_proxy": "http://proxy-2"}
        with patch.dict(gopay_app.os.environ, {"GOPAY_PROXY_POOL": "http://proxy-1,http://proxy-2,http://proxy-3"}):
            first = gopay_app.gopay_proxy_for_attempt(1, state)
            second = gopay_app.gopay_proxy_for_attempt(2, state)
            third = gopay_app.gopay_proxy_for_attempt(3, state)

        self.assertEqual(first[0], "http://proxy-2")
        self.assertEqual(second[0], "http://proxy-3")
        self.assertEqual(third[0], "http://proxy-1")
        self.assertNotIn("_gopay_proxy_first", state)

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_check_phone_returns_registered_from_login_methods(self):
        with patch.object(app_server, "load_state", side_effect=AssertionError("state should not be loaded")), \
                patch.object(app_server, "save_state", side_effect=AssertionError("state should not be saved")), \
                patch.object(app_server, "check_phone_by_login_methods", return_value={
                    "success": True,
                    "available": False,
                    "status": "registered",
                }) as check:
            resp = app_server.GopayAppServicer().CheckPhone(
                app_server.gopay_app_pb2.CheckPhoneRequest(phone="8000000000"),
                None,
            )

        self.assertFalse(resp.available)
        self.assertEqual(resp.status, "registered")
        self.assertEqual(resp.error_message, "PHONE_REGISTERED")
        check.assert_called_once_with("8000000000", "+62")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_check_phone_returns_available_without_prechecking_change_phone(self):
        with patch.object(app_server, "load_state", side_effect=AssertionError("state should not be loaded")), \
                patch.object(app_server, "save_state", side_effect=AssertionError("state should not be saved")), \
                patch.object(app_server, "check_phone_by_login_methods", return_value={
                    "success": True,
                    "available": True,
                    "status": "available",
                }) as check:
            resp = app_server.GopayAppServicer().CheckPhone(
                app_server.gopay_app_pb2.CheckPhoneRequest(phone="8000000000"),
                None,
            )

        self.assertTrue(resp.available)
        self.assertEqual(resp.status, "available")
        check.assert_called_once_with("8000000000", "+62")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_change_phone_start_uses_activity_v5_customers_flow(self):
        state = {
            "stage": "ready",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "email": "a@aa.cc",
            "name": "gg",
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def post(self, url, body=None, **kwargs):
                calls.append(("post", url, body, kwargs))
                if "account-recovery/account-list" in url:
                    return {"status": 200, "data": {"accounts": []}}
                raise AssertionError(f"unexpected post {url}")

            def get(self, url, **kwargs):
                calls.append(("get", url))
                if url.endswith("/gojek/v2/customer"):
                    return {
                        "status": 200,
                        "data": {
                            "customer": {
                                "name": "Current Name",
                                "email": "current@example.test",
                                "phone": TEST_E164_PHONE,
                            },
                        },
                    }
                raise AssertionError(f"unexpected get {url}")

            def patch(self, url, body=None, extra_headers=None, **kwargs):
                calls.append(("patch", url, body, extra_headers or {}))
                if not extra_headers:
                    return {"status": 461, "data": {"errors": [{"code": "CO:CUST:pin_verification"}]}}
                return {
                    "status": 200,
                    "data": {
                        "code": "CO:CUST:sms_verification",
                        "otp_token": "otp-token",
                        "expires_in": 300,
                    },
                }

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "ensure_access_token", lambda target: {"success": True}), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().ChangePhoneStart(
                app_server.gopay_app_pb2.ChangePhoneStartRequest(pin=TEST_PIN, new_phone=TEST_CHANGE_FULL_PHONE),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(resp.new_phone, TEST_CHANGE_LOCAL_PHONE)
        self.assertEqual(state["_change_otp_token"], "otp-token")
        patch_urls = [call[1] for call in calls if call[0] == "patch"]
        self.assertEqual(patch_urls, [
            "https://api.gojekapi.com/v5/customers",
            "https://api.gojekapi.com/v5/customers",
        ])
        patch_bodies = [call[2] for call in calls if call[0] == "patch"]
        self.assertEqual(patch_bodies[0]["name"], "Current Name")
        self.assertEqual(patch_bodies[0]["email"], "current@example.test")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_change_phone_start_generates_profile_email_when_current_profile_has_none(self):
        state = {
            "stage": "ready",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "name": "gg",
            "device": {},
        }
        patch_bodies = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                self.token = token

            def get(self, url, **kwargs):
                if url.endswith("/gojek/v2/customer"):
                    return {
                        "status": 200,
                        "data": {
                            "customer": {
                                "name": "Current Name",
                                "phone": TEST_E164_PHONE,
                            },
                        },
                    }
                raise AssertionError(f"unexpected get {url}")

            def patch(self, url, body=None, extra_headers=None, **kwargs):
                patch_bodies.append(body)
                if not extra_headers:
                    return {"status": 461, "data": {"errors": [{"code": "CO:CUST:pin_verification"}]}}
                return {
                    "status": 200,
                    "data": {
                        "code": "CO:CUST:sms_verification",
                        "otp_token": "otp-token",
                        "expires_in": 300,
                    },
                }

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "ensure_access_token", lambda target: {"success": True}), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().ChangePhoneStart(
                app_server.gopay_app_pb2.ChangePhoneStartRequest(pin=TEST_PIN, new_phone=TEST_CHANGE_FULL_PHONE),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(state["email"], f"gopay62{TEST_CHANGE_LOCAL_PHONE}@gmail.com")
        self.assertEqual(patch_bodies[0]["email"], state["email"])

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_change_phone_retry_uses_activity_otp_retry(self):
        state = {
            "stage": "change_phone_otp_pending",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "_change_phone": "89611122227",
            "_change_otp_token": "old-token",
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                pass

            def post(self, url, body=None, **kwargs):
                calls.append((url, body))
                return {"status": 200, "data": {"otp_token": "new-token", "otp_expires_in": 300}}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "ensure_access_token", lambda target: {"success": True}), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().ChangePhoneRetry(
                app_server.gopay_app_pb2.ChangePhoneRetryRequest(),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(calls, [("https://api.gojekapi.com/v2/otp/retry", {
            "otp_token": "old-token",
            "channel_type": "sms",
        })])
        self.assertEqual(state["_change_otp_token"], "new-token")

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_change_phone_complete_uses_activity_v5_verify_without_country_sync_by_default(self):
        state = {
            "stage": "change_phone_otp_pending",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "refresh_token": "seed-refresh-token",
            "token_expires_at": int(time.time()) + 3600,
            "_change_phone": "89611122227",
            "_change_otp_token": "otp-token",
            "device": {},
        }
        calls = []

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                pass

            def post(self, url, body=None, **kwargs):
                calls.append(("post", url, body))
                return {"status": 200, "data": {"message": "Phone verification was successful!"}}

            def get(self, url, **kwargs):
                calls.append(("get", url))
                if url.endswith("/gojek/v2/customer"):
                    return {
                        "status": 200,
                        "data": {"customer": {"phone": "+6289611122227", "number": "89611122227"}},
                    }
                raise AssertionError(f"unexpected get {url}")

            def put(self, url, body=None, **kwargs):
                calls.append(("put", url, body))
                return {"status": 200, "data": {}}

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "GOPAY_CHANGE_PHONE_COUNTRY_SYNC", False), \
                patch.object(app_server, "ensure_access_token", lambda target: {"success": True}), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().ChangePhoneComplete(
                app_server.gopay_app_pb2.ChangePhoneCompleteRequest(otp="1234"),
                None,
            )

        self.assertTrue(resp.success)
        self.assertEqual(calls, [
            ("post", "https://api.gojekapi.com/v5/customers/verificationUpdateProfile", {
                "otp": "1234",
                "otp_token": "otp-token",
            }),
            ("get", "https://api.gojekapi.com/gojek/v2/customer"),
        ])
        self.assertEqual(state["phone"], "89611122227")
        self.assertNotIn("token", state)
        self.assertNotIn("refresh_token", state)
        self.assertNotIn("token_expires_at", state)
        self.assertIn("_tmp_token", state)
        self.assertEqual(state["_tmp_refresh_token"], "seed-refresh-token")
        self.assertEqual(state["_tmp_phone"], "89611122227")
        self.assertNotIn("_change_otp_token", state)

    @unittest.skipIf(app_server is None, f"app_server import failed: {APP_SERVER_IMPORT_ERROR}")
    def test_change_phone_complete_fails_when_profile_does_not_confirm_new_phone(self):
        state = {
            "stage": "change_phone_otp_pending",
            "token": jwt_with_exp(int(time.time()) + 3600),
            "refresh_token": "seed-refresh-token",
            "token_expires_at": int(time.time()) + 3600,
            "_change_phone": "89611122227",
            "_change_otp_token": "otp-token",
            "device": {},
        }

        class FakeClient:
            def __init__(self, token, proxy=None, device=None):
                pass

            def post(self, url, body=None, **kwargs):
                return {"status": 200, "data": {"message": "Phone verification was successful!"}}

            def get(self, url, **kwargs):
                if url.endswith("/gojek/v2/customer"):
                    return {
                        "status": 200,
                        "data": {"customer": {"phone": "+6285800000940", "number": "85800000940"}},
                    }
                raise AssertionError(f"unexpected get {url}")

        def fake_load_state():
            return dict(state)

        def fake_save_state(next_state):
            state.clear()
            state.update(next_state)

        with patch.object(app_server, "load_state", fake_load_state), \
                patch.object(app_server, "save_state", fake_save_state), \
                patch.object(app_server, "GopayClient", FakeClient), \
                patch.object(app_server, "GOPAY_CHANGE_PHONE_CONFIRM_TIMEOUT_SECONDS", 0), \
                patch.object(app_server, "GOPAY_CHANGE_PHONE_COUNTRY_SYNC", False), \
                patch.object(app_server, "ensure_access_token", lambda target: {"success": True}), \
                patch.object(app_server, "access_token_usable", lambda target, min_ttl=30: True):
            resp = app_server.GopayAppServicer().ChangePhoneComplete(
                app_server.gopay_app_pb2.ChangePhoneCompleteRequest(otp="1234"),
                None,
            )

        self.assertFalse(resp.success)
        self.assertIn("phone change not confirmed", resp.error_message)
        self.assertEqual(state["stage"], "change_phone_otp_pending")
        self.assertIn("_change_otp_token", state)
        self.assertIn("token", state)


if __name__ == "__main__":
    unittest.main()
