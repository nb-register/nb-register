import unittest
import threading

from browser_reg.cookies import extract_session_token
from browser_reg.flow import _apply_plus_trial_probe_result, _select_checkout_amount
from browser_reg.server import BrowserFlow


class _BrowserFlowRequest:
    job_id = "job-1"
    assigned_email = "user@example.com"
    password = "password"
    first_name = "First"
    last_name = "Last"
    birthday = "01/01/1990"


class PlusTrialProbeTests(unittest.TestCase):
    def test_extracts_chunked_session_cookie(self):
        token = extract_session_token([
            {"name": "__Secure-next-auth.session-token.1", "value": "tail", "domain": ".chatgpt.com"},
            {"name": "__Secure-next-auth.session-token.0", "value": "head", "domain": ".chatgpt.com"},
        ])

        self.assertEqual(token, "headtail")

    def test_extracts_authjs_session_cookie(self):
        token = extract_session_token([
            {"name": "__Secure-authjs.session-token", "value": "session", "domain": ".chatgpt.com"},
        ])

        self.assertEqual(token, "session")

    def test_prefers_stripe_total_summary_due(self):
        amount, source = _select_checkout_amount({
            "invoice": {"amount_due": 34900000},
            "total_summary": {"due": 0},
        })

        self.assertEqual(amount, 0)
        self.assertEqual(source, "total_summary.due")

    def test_reads_invoice_amount_due(self):
        amount, source = _select_checkout_amount({
            "currency": "idr",
            "invoice": {"amount_due": 34900000},
        })

        self.assertEqual(amount, 34900000)
        self.assertEqual(source, "invoice.amount_due")

    def test_browser_probe_marks_nonzero_ineligible(self):
        result = {
            "plus_trial": False,
            "plus_trial_checked": False,
            "plus_trial_amount": 0,
            "plus_trial_currency": "",
            "plus_trial_source": "",
            "checkout_url": "",
        }

        _apply_plus_trial_probe_result(result, {
            "status": 200,
            "stripe_init_status": 200,
            "checkout_session_id": "cs_live_123",
            "url": "https://chatgpt.com/checkout/openai_llc/cs_live_123",
            "stripe_init": {
                "currency": "idr",
                "total_summary": {"due": 34900000},
            },
        })

        self.assertTrue(result["plus_trial_checked"])
        self.assertFalse(result["plus_trial"])
        self.assertEqual(result["plus_trial_amount"], 34900000)
        self.assertEqual(result["plus_trial_currency"], "IDR")
        self.assertEqual(result["plus_trial_source"], "total_summary.due")

    def test_browser_probe_marks_zero_eligible(self):
        result = {
            "plus_trial": False,
            "plus_trial_checked": False,
            "plus_trial_amount": 0,
            "plus_trial_currency": "",
            "plus_trial_source": "",
            "checkout_url": "",
        }

        _apply_plus_trial_probe_result(result, {
            "status": 200,
            "stripe_init_status": 200,
            "checkout_session_id": "cs_live_123",
            "stripe_init": {
                "currency": "idr",
                "total_summary": {"due": 0, "total": 34900000},
            },
        })

        self.assertTrue(result["plus_trial_checked"])
        self.assertTrue(result["plus_trial"])
        self.assertEqual(result["plus_trial_amount"], 0)


class BrowserFlowOTPTriggerTests(unittest.TestCase):
    def make_flow(self) -> BrowserFlow:
        return BrowserFlow(_BrowserFlowRequest(), threading.Event(), mode="login")

    def test_waiting_uses_request_action_start_time(self):
        flow = self.make_flow()

        flow._on_status_change("OTP_REQUEST_CLICK")
        action_started_at = flow.otp_request_action_started_at_unix
        flow._on_status_change("WAITING_FOR_OTP")

        self.assertGreater(action_started_at, 0)
        self.assertEqual(flow.otp_issued_after_unix, action_started_at)
        self.assertGreater(flow.otp_wait_started_at_unix, 0)

    def test_waiting_without_trigger_does_not_fallback_to_wait_time(self):
        flow = self.make_flow()

        flow._on_status_change("WAITING_FOR_OTP")

        self.assertEqual(flow.otp_issued_after_unix, 0)
        self.assertGreater(flow.otp_wait_started_at_unix, 0)

    def test_later_request_action_does_not_overwrite_first_start_time(self):
        flow = self.make_flow()

        flow._on_status_change("OTP_REQUEST_CLICK")
        first_action_started_at = flow.otp_request_action_started_at_unix
        flow._on_status_change("OTP_REQUEST_CLICK")

        self.assertGreater(flow.otp_issued_after_unix, 0)
        self.assertEqual(flow.otp_request_action_started_at_unix, first_action_started_at)
        self.assertEqual(flow.otp_issued_after_unix, first_action_started_at)


if __name__ == "__main__":
    unittest.main()
