import threading
import unittest

from payment_server import OtpStore


class OtpStoreTests(unittest.TestCase):
    def test_gopay_wait_ignores_non_gopay_and_returns_whatsapp(self):
        store = OtpStore()
        store.submit("111111", source="短信", issued_at_unix=100, hint="OpenAI code 111111")

        timer = threading.Timer(
            0.05,
            lambda: store.submit(
                "222222",
                source="WhatsApp",
                issued_at_unix=101,
                hint="GoPay verification code 222222",
            ),
        )
        timer.start()
        try:
            item = store.wait(
                timeout_seconds=1,
                issued_after_unix=100,
                is_active=lambda: True,
                purpose="gopay",
            )
        finally:
            timer.cancel()

        self.assertIsNotNone(item)
        self.assertEqual(item["otp"], "222222")
        self.assertEqual(item["source"], "WhatsApp")

    def test_gopay_wait_accepts_sms_source_when_payload_mentions_gopay(self):
        store = OtpStore()
        store.submit("333333", source="短信", issued_at_unix=100, hint="GoPay verification code 333333")

        item = store.wait(
            timeout_seconds=1,
            issued_after_unix=100,
            is_active=lambda: True,
            purpose="gopay",
        )

        self.assertIsNotNone(item)
        self.assertEqual(item["otp"], "333333")

if __name__ == "__main__":
    unittest.main()
