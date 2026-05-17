import logging
import os
import platform as _platform
import random
import shutil
import tempfile
import time
from typing import Callable, Optional
from urllib.parse import urlparse

from browserforge.fingerprints import Screen
from camoufox.sync_api import Camoufox

from browser_reg.cookies import extract_session_token
from browser_reg.flow import (
    BrowserRegistrationCancelled,
    _is_playwright_target_closed_error,
    apply_browser_language_overrides,
    browser_accept_language,
    browser_firefox_user_prefs,
    browser_languages,
    browser_locale,
    browser_process_env,
    browser_timezone,
    browser_window_size,
    cleanup_stale_browser_profiles,
)
from browser_reg.sensitive import redact_email, sanitize_text, sanitize_url_for_log

logger = logging.getLogger(__name__)


def _interruptible_sleep(seconds: float, check_cancel: Callable[[], None]) -> None:
    deadline = time.time() + max(0.0, seconds)
    while True:
        check_cancel()
        remaining = deadline - time.time()
        if remaining <= 0:
            return
        time.sleep(min(0.25, remaining))


def _env_bool(name: str, default: bool) -> bool:
    value = os.environ.get(name, "").strip().lower()
    if not value:
        return default
    return value not in ("0", "false", "no", "off")


def browser_login(
    email: str,
    password: str,
    proxy: str,
    wait_for_otp_fn,
    on_status_change_fn,
    should_cancel_fn: Optional[Callable[[], bool]] = None,
) -> dict:
    logger.info("[browser-reg] Login account: %s", redact_email(email))

    cf_proxy = None
    if proxy:
        pp = urlparse(proxy)
        cf_proxy = {
            "server": f"{pp.scheme}://{pp.hostname}:{pp.port}",
            "username": pp.username or "",
            "password": pp.password or "",
        }

    screenshot_dir = os.environ.get("SCREENSHOT_DIR", "/tmp/screenshots")
    os.makedirs(screenshot_dir, exist_ok=True)
    cleanup_stale_browser_profiles(4 * 3600)

    tmp_profile = tempfile.mkdtemp(prefix="chatgpt_login_")
    logger.info("[browser-reg] Temp login profile: %s", tmp_profile)

    result = {
        "email": email,
        "password": password,
        "session_token": "",
        "access_token": "",
        "device_id": "",
        "csrf_token": "",
        "cookie_header": "",
        "plus_trial": False,
        "plus_trial_checked": False,
        "plus_trial_amount": 0,
        "plus_trial_currency": "",
        "plus_trial_source": "",
        "checkout_url": "",
    }

    def check_cancel() -> None:
        if should_cancel_fn and should_cancel_fn():
            raise BrowserRegistrationCancelled("browser login cancelled")

    def sleep(seconds: float) -> None:
        _interruptible_sleep(float(seconds), check_cancel)

    ctx = None
    page = None

    def active_page():
        nonlocal page
        if page is not None:
            try:
                if not page.is_closed():
                    return page
            except Exception as e:
                if not _is_playwright_target_closed_error(e):
                    raise

        if ctx is None:
            raise RuntimeError("browser context is not available")

        try:
            pages = [p for p in ctx.pages if not p.is_closed()]
        except Exception as e:
            raise RuntimeError(f"browser context is closed: {sanitize_text(e)}") from e
        if not pages:
            raise RuntimeError("browser page closed and no replacement page is available")

        page = pages[-1]
        logger.info("[browser-reg] Switched to active login browser page")
        return page

    def with_active_page(action):
        nonlocal page
        last_error = None
        for attempt in range(2):
            try:
                return action(active_page())
            except Exception as e:
                if attempt == 0 and _is_playwright_target_closed_error(e):
                    last_error = e
                    page = None
                    continue
                raise
        raise last_error

    def query_selector(selector: str):
        return with_active_page(lambda p: p.query_selector(selector))

    def page_url() -> str:
        return with_active_page(lambda p: p.url)

    def page_evaluate(script: str, *args):
        return with_active_page(lambda p: p.evaluate(script, *args))

    def page_screenshot(path: str) -> bool:
        try:
            with_active_page(lambda p: p.screenshot(path=path))
            return True
        except Exception as e:
            logger.info("[browser-reg] Login screenshot failed: %s", sanitize_text(e))
            return False

    def safe_click(element, label: str, timeout: int = 5000) -> bool:
        try:
            element.click(timeout=timeout, force=True)
            return True
        except Exception as e:
            logger.info("[browser-reg] %s click failed, trying JS click: %s", label, sanitize_text(e))
        try:
            element.evaluate("el => el.click()")
            return True
        except Exception as e:
            logger.warning("[browser-reg] %s JS click failed: %s", label, sanitize_text(e))
            return False

    def fill_input(element, value: str) -> bool:
        try:
            element.focus()
            sleep(0.1)
            element.fill(value, timeout=5000)
            return True
        except Exception:
            pass
        try:
            element.evaluate(
                """(el, value) => {
                    el.focus();
                    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
                    if (setter) setter.call(el, value); else el.value = value;
                    el.dispatchEvent(new Event("input", {bubbles: true}));
                    el.dispatchEvent(new Event("change", {bubbles: true}));
                }""",
                value,
            )
            return True
        except Exception as e:
            logger.warning("[browser-reg] input fill failed: %s", sanitize_text(e))
            return False

    def find_otp_input():
        for sel in [
            'input[autocomplete="one-time-code"]:visible',
            'input[name="code"]:visible',
            'input[inputmode="numeric"]:visible',
            'input[aria-label*="code" i]:visible',
            'input[placeholder*="code" i]:visible',
            'input[type="text"]:visible',
        ]:
            try:
                el = query_selector(sel)
                if el and el.is_visible():
                    return el
            except Exception:
                continue
        return None

    def find_email_input():
        for sel in [
            'input[type="email"]:visible',
            'input[name="email"]:visible',
            'input[name*="email" i]:visible',
            'input[autocomplete="email"]:visible',
            'input[placeholder*="email" i]:visible',
            'input[aria-label*="email" i]:visible',
            'input[name="username"]:visible',
        ]:
            try:
                el = query_selector(sel)
                if el and el.is_visible():
                    return el
            except Exception:
                continue
        return None

    def input_value(element) -> str:
        try:
            value = element.evaluate("el => el.value || ''")
            return value.strip() if isinstance(value, str) else ""
        except Exception:
            return ""

    def fill_email_input() -> bool:
        target = email.strip()
        for attempt in range(4):
            check_cancel()
            email_input = find_email_input()
            if not email_input:
                sleep(0.5)
                continue
            if not fill_input(email_input, email):
                sleep(0.5)
                continue
            sleep(0.2)
            if input_value(email_input) == target:
                return True
            refreshed = find_email_input()
            if refreshed and input_value(refreshed) == target:
                return True
            logger.info("[browser-reg] Login email field did not retain value, retry %s/4", attempt + 1)
            sleep(0.5)
        return False

    def click_email_continue(label: str) -> bool:
        try:
            on_status_change_fn("OTP_REQUEST_CLICK")
        except Exception as e:
            logger.info("[browser-reg] Login OTP request callback failed: %s", sanitize_text(e))
        try:
            clicked = page_evaluate('''() => {
                const visible = (el) => {
                    const rect = el.getBoundingClientRect();
                    const style = window.getComputedStyle(el);
                    return rect.width > 0
                        && rect.height > 0
                        && style.visibility !== 'hidden'
                        && style.display !== 'none';
                };
                const candidates = document.querySelectorAll(
                    'button, input[type="submit"], a, div[role="button"]'
                );
                for (const el of candidates) {
                    const text = (el.innerText || el.textContent || el.value || '').trim();
                    const disabled = el.disabled || el.getAttribute('aria-disabled') === 'true';
                    if (!disabled && visible(el) && /^(continue|next)$/i.test(text)) {
                        el.click();
                        return true;
                    }
                }

                const email = document.querySelector(
                    'input[type="email"], input[name="email"], input[name*="email" i], ' +
                    'input[autocomplete="email"], input[placeholder*="email" i], ' +
                    'input[aria-label*="email" i], input[name="username"]'
                );
                if (email && email.form) {
                    if (email.form.requestSubmit) {
                        email.form.requestSubmit();
                    } else {
                        email.form.submit();
                    }
                    return true;
                }
                return false;
            }''')
            if clicked:
                on_status_change_fn("OTP_REQUEST_CLICKED")
                logger.info("[browser-reg] %s: JS form submit", label)
                return True
        except Exception as e:
            logger.info("[browser-reg] %s JS submit failed: %s", label, sanitize_text(e))

        try:
            with_active_page(lambda p: p.keyboard.press("Enter"))
            on_status_change_fn("OTP_REQUEST_CLICKED")
            logger.info("[browser-reg] %s: keyboard Enter", label)
            return True
        except Exception as e:
            logger.info("[browser-reg] %s keyboard submit failed: %s", label, sanitize_text(e))
            return False

    def submit_email_entry(label: str) -> bool:
        if not fill_email_input():
            return False
        sleep(random.uniform(0.2, 0.5))
        return click_email_continue(label)

    def click_login_entry() -> bool:
        for sel in [
            'a[data-testid="login-button"]',
            'button[data-testid="login-button"]',
            'button:has-text("Log in")',
            'a:has-text("Log in")',
        ]:
            try:
                btn = query_selector(sel)
                if btn and btn.is_visible() and safe_click(btn, "Login"):
                    logger.info("[browser-reg] Clicked login: %s", sel)
                    return True
            except Exception:
                continue
        return False

    def wait_for_email_input(seconds: int = 60):
        for i in range(max(1, seconds)):
            email_input = find_email_input()
            if email_input:
                return email_input
            if i in (0, 10, 25, 40):
                click_login_entry()
            sleep(1)
        return None

    def click_password_flow() -> bool:
        for sel in [
            'button:has-text("Continue with password")',
            'a:has-text("Continue with password")',
            'button:has-text("continue with password")',
            'button:has-text("Use password")',
            'a:has-text("Use password")',
            'button:has-text("Password")',
            'a:has-text("Password")',
        ]:
            try:
                el = query_selector(sel)
                if el and el.is_visible() and safe_click(el, "Login password flow"):
                    logger.info("[browser-reg] Switched login to password flow: %s", sel)
                    return True
            except Exception:
                continue
        try:
            return bool(page_evaluate('''() => {
                const els = document.querySelectorAll('a, button, div[role="button"]');
                for (const el of els) {
                    const t = (el.textContent || '').trim().toLowerCase();
                    if ((t === 'continue with password' || t === 'use password' || t === 'password') && el.offsetParent !== null) {
                        el.click();
                        return true;
                    }
                }
                return false;
            }'''))
        except Exception:
            return False

    def submit_login_otp() -> bool:
        logger.info("[browser-reg] Login OTP page reached")
        on_status_change_fn("WAITING_FOR_OTP")
        otp_code = wait_for_otp_fn()
        if not otp_code:
            raise RuntimeError("OTP is empty")
        otp_input = find_otp_input()
        if not otp_input or not fill_input(otp_input, otp_code):
            logger.info("[browser-reg] Login OTP input changed while filling; checking for session")
            if wait_for_session("login-otp-fill-navigation", seconds=15):
                return True
            otp_input = find_otp_input()
            if not otp_input or not fill_input(otp_input, otp_code):
                raise RuntimeError("login OTP input not found")
        for sel in ['button[type="submit"]', 'button:has-text("Continue")', 'button:has-text("Verify")']:
            b = query_selector(sel)
            if b and b.is_visible() and safe_click(b, "Login OTP continue"):
                break
        return wait_for_session("login-otp-submit", seconds=15)

    def capture_session_state(label: str) -> bool:
        session_info = {}
        try:
            session_info = page_evaluate(
                """async () => {
                    const controller = new AbortController();
                    const timer = setTimeout(() => controller.abort(), 8000);
                    try {
                        const r = await fetch("/api/auth/session", {
                            credentials: "include",
                            signal: controller.signal
                        });
                        return await r.json();
                    } finally {
                        clearTimeout(timer);
                    }
                }"""
            )
        except Exception as e:
            logger.info("[browser-reg] Login session fetch failed at %s: %s", label, sanitize_text(e))

        if not (isinstance(session_info, dict) and session_info.get("accessToken")):
            try:
                direct_resp = ctx.request.get("https://chatgpt.com/api/auth/session", timeout=15000)
                direct_info = direct_resp.json() if direct_resp.ok else {}
                if isinstance(direct_info, dict):
                    session_info = direct_info
            except Exception as e:
                logger.info("[browser-reg] Login session page read failed at %s: %s", label, sanitize_text(e))

        if isinstance(session_info, dict) and session_info.get("accessToken"):
            result["access_token"] = session_info.get("accessToken", "")

        try:
            cookies = ctx.cookies()
        except Exception as e:
            logger.info("[browser-reg] Login cookie capture failed at %s: %s", label, sanitize_text(e))
            cookies = []
        chatgpt_cookies = [c for c in cookies if "chatgpt.com" in c.get("domain", "")]
        if _env_bool("BROWSER_REG_DEBUG", False):
            logger.info(
                "[browser-reg] Login cookie names: %s",
                ", ".join(sorted({str(c.get("name", "")) for c in chatgpt_cookies if c.get("name")})),
            )
        result["session_token"] = extract_session_token(chatgpt_cookies)
        for c in chatgpt_cookies:
            name = c["name"]
            if name in ("oai-did", "oai-device-id"):
                result["device_id"] = c["value"]
            if name == "__Host-next-auth.csrf-token":
                val = c["value"]
                result["csrf_token"] = val.split("|")[0] if "|" in val else val
        if chatgpt_cookies:
            result["cookie_header"] = "; ".join(f"{c['name']}={c['value']}" for c in chatgpt_cookies)
        logger.info(
            "[browser-reg] Login session_token=%s access_token=%s device_id=%s",
            "yes" if result["session_token"] else "no",
            "yes" if result["access_token"] else "no",
            "yes" if result["device_id"] else "no",
        )
        return bool(result["access_token"] or result["session_token"])

    def wait_for_session(label: str, seconds: int = 20) -> bool:
        for _ in range(max(1, seconds)):
            sleep(1)
            try:
                cur = page_url()
            except Exception:
                return False
            if "chatgpt.com" in cur and "auth.openai.com" not in cur and capture_session_state(label):
                return True
        return False

    try:
        debug_mode = _env_bool("BROWSER_REG_DEBUG", False)
        headless = False if debug_mode else ("virtual" if _platform.system() == "Linux" else False)
        geoip_enabled = _env_bool("CAMOUFOX_GEOIP", True)
        locale = browser_locale()
        languages = browser_languages()
        timezone = browser_timezone()
        window_width, window_height = browser_window_size()
        block_images = _env_bool("BROWSER_REG_BLOCK_IMAGES", False)
        if debug_mode:
            logger.info("[browser-reg] Debug mode enabled: headless=False")
            logger.info(
                "[browser-reg] Language override: locale=%s languages=%s timezone=%s",
                locale,
                languages,
                timezone or "geoip",
            )
        camoufox_options = {
            "headless": headless,
            "humanize": True,
            "persistent_context": True,
            "user_data_dir": tmp_profile,
            "screen": Screen(max_width=window_width, max_height=window_height),
            "window": (window_width, window_height),
            "block_images": block_images,
            "proxy": cf_proxy,
            "geoip": geoip_enabled,
            "locale": languages,
            "extra_http_headers": {"Accept-Language": browser_accept_language()},
            "firefox_user_prefs": browser_firefox_user_prefs(),
            "env": browser_process_env(),
        }
        if timezone:
            camoufox_options["timezone_id"] = timezone

        with Camoufox(**camoufox_options) as ctx:
            apply_browser_language_overrides(ctx)
            page = ctx.pages[0] if ctx.pages else ctx.new_page()
            logger.info("[browser-reg] Opening chatgpt.com for login ...")
            with_active_page(lambda p: p.goto("https://chatgpt.com/", wait_until="domcontentloaded", timeout=60000))
            sleep(3)

            click_login_entry()
            if not wait_for_email_input() or not submit_email_entry("Login email continue"):
                page_screenshot(path=f"{screenshot_dir}/login_no_email.png")
                raise RuntimeError("login email input not found")

            password_ready = False
            email_resubmits = 0
            for i in range(60):
                sleep(1)
                if query_selector('input[type="password"]:visible') or query_selector('input[name="password"]:visible'):
                    password_ready = True
                    break
                cur = page_url()
                if "chatgpt.com" in cur and "auth.openai.com" not in cur and capture_session_state("login-email-arrival"):
                    return result
                if email_resubmits < 2 and find_email_input():
                    email_resubmits += 1
                    logger.info(
                        "[browser-reg] Still on login email entry after submit, retrying %s/2",
                        email_resubmits,
                    )
                    if submit_email_entry(f"Login email continue retry {email_resubmits}"):
                        sleep(2)
                        continue
                if click_password_flow():
                    sleep(2)
                    continue
                if find_otp_input():
                    if submit_login_otp():
                        return result
                    sleep(2)
                    continue
                if i in (15, 30, 45):
                    logger.info("[browser-reg] Waiting for login password field, URL: %s", sanitize_url_for_log(page_url()))

            if not password_ready:
                page_screenshot(path=f"{screenshot_dir}/login_no_password.png")
                raise RuntimeError("login password input not found")

            pwd_input = query_selector('input[type="password"]') or query_selector('input[name="password"]')
            if not pwd_input or not fill_input(pwd_input, password):
                raise RuntimeError("login password input not found")
            for sel in ['button[type="submit"]', 'button:has-text("Continue")', 'button:has-text("Next")']:
                b = query_selector(sel)
                if b and b.is_visible():
                    on_status_change_fn("OTP_REQUEST_CLICK")
                    if safe_click(b, "Login password continue"):
                        on_status_change_fn("OTP_REQUEST_CLICKED")
                        break

            for i in range(120):
                sleep(1)
                cur = page_url()
                if "chatgpt.com" in cur and "auth.openai.com" not in cur and capture_session_state("login-arrival"):
                    return result
                if find_otp_input():
                    if submit_login_otp():
                        return result
                if i in (30, 60):
                    logger.info("[browser-reg] Login wait URL: %s", sanitize_url_for_log(cur))

            page_screenshot(path=f"{screenshot_dir}/login_no_session.png")
            raise RuntimeError("login did not produce session")
    finally:
        shutil.rmtree(tmp_profile, ignore_errors=True)
        logger.info("[browser-reg] Temp login profile removed: %s", tmp_profile)
