import unittest
from unittest.mock import patch

from gopay import (
    GoPayCharger,
    GoPayError,
    GoPayOTPRejected,
    _extract_midtrans_charge_reference,
    _request_with_retries,
    _resolve_expected_amount,
    _stripe_confirm_error_detail,
    probe_plus_trial_checkout,
)


class FakeResponse:
    def __init__(self, status_code, text="", payload=None):
        self.status_code = status_code
        self.text = text
        self._payload = payload or {}

    def json(self):
        return self._payload

    def raise_for_status(self):
        if self.status_code >= 400:
            raise GoPayError(f"http {self.status_code}: {self.text}")


class FakeExt:
    def __init__(self, response):
        self.response = response
        self.headers = {}
        self.proxies = {}

    def post(self, *args, **kwargs):
        return self.response

    def close(self):
        pass


class FakeChatGPTSession:
    def __init__(self):
        self.headers = {"User-Agent": "test-agent"}
        self.proxies = {}
        self.closed = False

    def post(self, *args, **kwargs):
        return FakeResponse(200, payload={"checkout_session_id": "cs_test_probe"})

    def close(self):
        self.closed = True


class FlakySession:
    def __init__(self):
        self.calls = 0

    def post(self, *args, **kwargs):
        self.calls += 1
        if self.calls == 1:
            raise RuntimeError("TLS connect error: transient")
        return FakeResponse(200, payload={"ok": True})


class GoPayValidateOtpTests(unittest.TestCase):
    def charger_for(self, response):
        charger = GoPayCharger.__new__(GoPayCharger)
        charger.ext = FakeExt(response)
        charger.browser_locale = "zh-CN"
        return charger

    def test_validate_otp_400_is_retryable_otp_error(self):
        charger = self.charger_for(FakeResponse(400, '{"success":false,"error":"invalid otp"}'))

        with self.assertRaises(GoPayOTPRejected) as raised:
            charger._gopay_validate_otp("ref", "111111")

        self.assertIn("validate-otp 400", str(raised.exception))
        self.assertIn("invalid otp", str(raised.exception))

    def test_validate_otp_unsuccessful_200_is_retryable_otp_error(self):
        charger = self.charger_for(FakeResponse(200, payload={"success": False, "error": "bad otp"}))

        with self.assertRaises(GoPayOTPRejected):
            charger._gopay_validate_otp("ref", "111111")


class RetryTransportTests(unittest.TestCase):
    def test_retries_retryable_transport_error(self):
        session = FlakySession()

        resp = _request_with_retries(
            session,
            "post",
            "https://api.stripe.com/v1/payment_methods",
            log=lambda _msg: None,
            delay_seconds=0,
        )

        self.assertEqual(resp.status_code, 200)
        self.assertEqual(session.calls, 2)


class MidtransChargeReferenceTests(unittest.TestCase):
    def test_extracts_reference_from_legacy_verification_link(self):
        ref = _extract_midtrans_charge_reference({
            "gopay_verification_link_url": "https://example.test/pay?reference=A123BC",
        })

        self.assertEqual(ref, "A123BC")

    def test_extracts_reference_from_nested_action_url(self):
        ref = _extract_midtrans_charge_reference({
            "actions": [
                {"name": "get-status", "url": "https://example.test/status"},
                {"name": "verify", "url": "gojek://pay#reference=A987ZZ"},
            ],
        })

        self.assertEqual(ref, "A987ZZ")

    def test_extracts_reference_from_explicit_reference_field(self):
        ref = _extract_midtrans_charge_reference({"payment_reference": "A555AA"})

        self.assertEqual(ref, "A555AA")


class StripeExpectedAmountTests(unittest.TestCase):
    def test_uses_zero_amount_from_checkout_session(self):
        amount, source = _resolve_expected_amount(
            {"currency": "idr", "checkout_session": {"amount_total": 0}},
            {},
        )

        self.assertEqual(amount, "0")
        self.assertEqual(source, "checkout_session.amount_total")

    def test_refuses_nonzero_amount_by_default(self):
        with self.assertRaises(GoPayError) as raised:
            _resolve_expected_amount(
                {"currency": "idr", "latest_invoice": {"amount_due": 319000}},
                {},
            )

        self.assertIn("not free-trial 0", str(raised.exception))

    def test_allows_nonzero_amount_when_explicitly_configured(self):
        amount, source = _resolve_expected_amount(
            {"currency": "idr", "latest_invoice": {"amount_due": 319000}},
            {"allow_nonzero_expected_amount": True},
        )

        self.assertEqual(amount, "319000")
        self.assertEqual(source, "latest_invoice.amount_due")

    def test_runtime_expected_amount_override(self):
        amount, source = _resolve_expected_amount(
            {"currency": "idr", "latest_invoice": {"amount_due": 319000}},
            {"expected_amount": "0"},
        )

        self.assertEqual(amount, "0")
        self.assertEqual(source, "runtime.expected_amount")

    def test_checkout_amount_mismatch_error_mentions_sent_amount(self):
        detail = _stripe_confirm_error_detail(
            '{"error":{"code":"checkout_amount_mismatch","message":"amount changed"}}',
            expected_amount="0",
            expected_amount_source="fallback_zero_unknown",
        )

        self.assertIn("sent expected_amount=0", detail)
        self.assertIn("another checkout", detail)


class PlusTrialProbeTests(unittest.TestCase):
    def test_probe_marks_zero_amount_eligible(self):
        stripe_init = FakeResponse(
            200,
            payload={
                "currency": "idr",
                "init_checksum": "chk",
                "payment_method_types": ["gopay"],
                "checkout_session": {"amount_total": 0},
            },
        )

        with patch("gopay._new_session", return_value=FakeExt(stripe_init)):
            result = probe_plus_trial_checkout(FakeChatGPTSession(), log=lambda _msg: None)

        self.assertTrue(result["checked"])
        self.assertTrue(result["plus_trial_eligible"])
        self.assertEqual(result["amount"], 0)
        self.assertEqual(result["source"], "checkout_session.amount_total")

    def test_probe_marks_nonzero_amount_ineligible(self):
        stripe_init = FakeResponse(
            200,
            payload={
                "currency": "idr",
                "init_checksum": "chk",
                "payment_method_types": ["gopay"],
                "latest_invoice": {"amount_due": 319000},
            },
        )

        with patch("gopay._new_session", return_value=FakeExt(stripe_init)):
            result = probe_plus_trial_checkout(FakeChatGPTSession(), log=lambda _msg: None)

        self.assertTrue(result["checked"])
        self.assertFalse(result["plus_trial_eligible"])
        self.assertEqual(result["amount"], 319000)


if __name__ == "__main__":
    unittest.main()
