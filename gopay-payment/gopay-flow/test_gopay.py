import unittest

from gopay import GoPayCharger, GoPayOTPRejected


class FakeResponse:
    def __init__(self, status_code, text="", payload=None):
        self.status_code = status_code
        self.text = text
        self._payload = payload or {}

    def json(self):
        return self._payload


class FakeExt:
    def __init__(self, response):
        self.response = response

    def post(self, *args, **kwargs):
        return self.response


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


if __name__ == "__main__":
    unittest.main()
