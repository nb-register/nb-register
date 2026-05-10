"""
Camoufox-based Outlook account registration flow.
Ported from: https://github.com/LainsNL/OutlookRegister

Usage:
    python -m browser_reg.outlook_flow --proxy socks5://127.0.0.1:10814
"""

import base64
import hashlib
import logging
import math
import os
import random
import re
import secrets
import shutil
import string
import tempfile
import time
from typing import Callable, Optional
from urllib.parse import parse_qs, urlencode, urlparse

import requests

logger = logging.getLogger(__name__)

BOT_PROTECTION_WAIT = 11  # seconds


# ---------------------------------------------------------------------------
# Data generators
# ---------------------------------------------------------------------------

def _gen_email_local() -> str:
    try:
        from faker import Faker
        fake = Faker("en_US")
        first = re.sub(r"[^a-z0-9]", "", fake.first_name().lower())
        last = re.sub(r"[^a-z0-9]", "", fake.last_name().lower())
        suffix = str(random.randint(10, 9999))
        candidates = [f"{first}{last}{suffix}", f"{first[0]}{last}{suffix}", f"{first}{last[0]}{suffix}"]
        return random.choice(candidates)[:24]
    except ImportError:
        chars = string.ascii_lowercase
        return "".join(random.choice(chars) for _ in range(random.randint(10, 14)))


def _gen_password() -> str:
    upper = random.choice(string.ascii_uppercase)
    lower = "".join(random.choice(string.ascii_lowercase) for _ in range(8))
    digits = "".join(random.choice(string.digits) for _ in range(3))
    special = random.choice("!@#$%&")
    pwd = list(upper + lower + digits + special)
    random.shuffle(pwd)
    return "".join(pwd)


def _gen_name() -> tuple[str, str]:
    try:
        from faker import Faker
        fake = Faker("en_US")
        return fake.first_name(), fake.last_name()
    except ImportError:
        firsts = ["James", "Emily", "Michael", "Sophia", "Oliver", "Emma"]
        lasts = ["Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia"]
        return random.choice(firsts), random.choice(lasts)


# ---------------------------------------------------------------------------
# Bezier mouse helpers
# ---------------------------------------------------------------------------

def _bezier_move(page, x1, y1, x2, y2):
    dist = math.sqrt((x2 - x1) ** 2 + (y2 - y1) ** 2)
    steps = max(12, min(40, int(dist / 8)))
    cx1 = x1 + (x2 - x1) * random.uniform(0.15, 0.4) + random.randint(-20, 20)
    cy1 = y1 + (y2 - y1) * random.uniform(0.0, 0.3) + random.randint(-20, 20)
    cx2 = x1 + (x2 - x1) * random.uniform(0.6, 0.85) + random.randint(-10, 10)
    cy2 = y1 + (y2 - y1) * random.uniform(0.7, 1.0) + random.randint(-10, 10)
    for i in range(steps + 1):
        t = i / steps
        u = 1 - t
        px = u**3 * x1 + 3 * u**2 * t * cx1 + 3 * u * t**2 * cx2 + t**3 * x2
        py = u**3 * y1 + 3 * u**2 * t * cy1 + 3 * u * t**2 * cy2 + t**3 * y2
        page.mouse.move(px, py)
        page.wait_for_timeout(random.randint(4, 16))


def _click_with_bezier(page, locator, hold_ms=0):
    box = locator.bounding_box(timeout=5000)
    if not box:
        raise TimeoutError("element has no bounding box")
    tx = box["x"] + box["width"] / 2 + random.randint(-5, 5)
    ty = box["y"] + box["height"] / 2 + random.randint(-5, 5)
    sx = tx + random.randint(-80, 80)
    sy = ty + random.randint(-40, 40)
    page.mouse.move(sx, sy)
    page.wait_for_timeout(random.randint(60, 200))
    _bezier_move(page, sx, sy, tx, ty)
    page.wait_for_timeout(random.randint(30, 100))
    if hold_ms > 0:
        page.mouse.down()
        page.wait_for_timeout(hold_ms + random.randint(-200, 400))
        page.mouse.up()
    else:
        page.mouse.click(tx, ty)


def _first_visible_locator(page, selectors, timeout=10000):
    deadline = time.time() + timeout / 1000
    last_error = None
    while time.time() < deadline:
        for selector in selectors:
            try:
                matches = page.locator(selector)
                for index in range(min(matches.count(), 8)):
                    locator = matches.nth(index)
                    if locator.is_visible():
                        return locator
            except Exception as exc:
                last_error = exc
        page.wait_for_timeout(250)
    raise TimeoutError(f"no visible locator found: {selectors}; last_error={last_error}")


def _type_into(locator, value, delay=0):
    locator.click(timeout=5000, force=True)
    try:
        locator.press("Control+A", timeout=2000)
        locator.press("Backspace", timeout=2000)
    except Exception:
        locator.fill("", timeout=3000)
    if delay > 0:
        locator.type(value, delay=delay, timeout=10000)
    else:
        locator.fill(value, timeout=10000)


def _click_primary(page, timeout=10000):
    button = _first_visible_locator(page, [
        '[data-testid="primaryButton"]',
        'button[type="submit"]',
        'button:has-text("下一步")',
        'button:has-text("Next")',
        'button:has-text("继续")',
        'button:has-text("Continue")',
    ], timeout=timeout)
    button.click(timeout=5000, force=True)


def _select_birth_value(page, field_name, value, option_texts):
    field = _first_visible_locator(page, [
        f'[name="{field_name}"]',
        f'select[name="{field_name}"]',
        f'[id="{field_name}"]',
        f'[aria-labelledby*="{field_name}"]',
    ], timeout=10000)
    try:
        field.select_option(value=value, timeout=1500)
        return
    except Exception:
        pass

    field.click(timeout=5000, force=True)
    option_selectors = [f'[role="option"]:text-is("{text}")' for text in option_texts]
    option_selectors.extend([f'option:text-is("{text}")' for text in option_texts])
    _first_visible_locator(page, option_selectors, timeout=10000).click(timeout=5000, force=True)


def _click_first_if_visible(page, selectors, timeout=1000) -> bool:
    try:
        _first_visible_locator(page, selectors, timeout=timeout).click(timeout=5000, force=True)
        return True
    except Exception:
        return False


def _oauth_code_from_url(current_url: str) -> tuple[str, str]:
    parsed = urlparse(current_url)
    query = parse_qs(parsed.query)
    fragment = parse_qs(parsed.fragment)
    params = {**fragment, **query}
    if "error" in params:
        error = params.get("error", ["oauth_error"])[0]
        description = params.get("error_description", [""])[0]
        return "", f"{error}: {description}".strip()
    return params.get("code", [""])[0], ""


def _pkce_pair() -> tuple[str, str]:
    verifier = secrets.token_urlsafe(64)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    challenge = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
    return verifier, challenge


def _complete_microsoft_oauth_page(
    page,
    email: str,
    password: str,
    redirect_url: str,
    timeout=160,
    captured_url_fn: Callable[[], str] | None = None,
) -> tuple[str, str]:
    deadline = time.time() + timeout
    last_url = ""
    while time.time() < deadline:
        if captured_url_fn:
            captured_url = captured_url_fn()
            if captured_url:
                code, error = _oauth_code_from_url(captured_url)
                if code or error:
                    return code, error

        current_url = page.url
        if current_url != last_url:
            last_url = current_url
            logger.info("[oauth] page url changed: %s", current_url.split("?", 1)[0])

        code, error = _oauth_code_from_url(current_url)
        if code:
            return code, ""
        if error:
            return "", error

        progressed = False

        if _click_first_if_visible(page, [
            f'[data-test-id="{email}"]',
            f'[aria-label*="{email}"]',
            f'div[role="button"]:has-text("{email}")',
            f'div:has-text("{email}")',
        ], timeout=700):
            progressed = True

        try:
            login = _first_visible_locator(page, [
                '[name="loginfmt"]',
                'input[type="email"]',
                'input[autocomplete="username"]',
            ], timeout=700)
            _type_into(login, email)
            _click_first_if_visible(page, [
                '#idSIButton9',
                'button[type="submit"]',
                'input[type="submit"]',
                'button:has-text("Next")',
                'button:has-text("下一步")',
            ], timeout=3000)
            progressed = True
        except Exception:
            pass

        try:
            password_input = _first_visible_locator(page, [
                '[name="passwd"]',
                'input[type="password"]',
                'input[autocomplete="current-password"]',
            ], timeout=700)
            _type_into(password_input, password)
            _click_first_if_visible(page, [
                '#idSIButton9',
                'button[type="submit"]',
                'input[type="submit"]',
                'button:has-text("Sign in")',
                'button:has-text("登录")',
                'button:has-text("下一步")',
            ], timeout=3000)
            progressed = True
        except Exception:
            pass

        if _click_first_if_visible(page, [
            '[data-testid="appConsentPrimaryButton"]',
            'button:has-text("Accept")',
            'button:has-text("接受")',
            'button:has-text("同意")',
            'button:has-text("Continue")',
            'button:has-text("继续")',
            '#idSIButton9',
            'input[type="submit"]',
        ], timeout=700):
            progressed = True

        if redirect_url in page.url:
            code, error = _oauth_code_from_url(page.url)
            if code or error:
                return code, error

        page.wait_for_timeout(900 if progressed else 500)

    return "", "OAuth browser flow timed out before authorization code was captured"


def outlook_oauth(
    email: str,
    password: str,
    proxy: str = "",
    client_id: str = "",
    redirect_url: str = "",
    scopes: list[str] | None = None,
) -> dict:
    from browserforge.fingerprints import Screen
    from camoufox.sync_api import Camoufox

    email = email.strip().lower()
    password = password.strip()
    client_id = client_id.strip()
    redirect_url = redirect_url.strip()
    scopes = [scope.strip() for scope in (scopes or []) if scope.strip()]
    result = {
        "success": False,
        "email": email,
        "refresh_token": "",
        "access_token": "",
        "error": "",
    }
    if not email or not password:
        result["error"] = "email and password are required for OAuth"
        return result
    if not client_id or not redirect_url or not scopes:
        result["error"] = "client_id, redirect_url, and scopes are required for OAuth"
        return result

    verifier, challenge = _pkce_pair()
    authorize_url = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?" + urlencode({
        "client_id": client_id,
        "response_type": "code",
        "redirect_uri": redirect_url,
        "response_mode": "query",
        "scope": " ".join(scopes),
        "prompt": "consent",
        "login_hint": email,
        "code_challenge": challenge,
        "code_challenge_method": "S256",
    })

    cf_proxy = None
    if proxy:
        pp = urlparse(proxy)
        cf_proxy = {"server": f"{pp.scheme}://{pp.hostname}:{pp.port}",
                    "username": pp.username or "", "password": pp.password or ""}

    ss_dir = (
        os.environ.get("SCREENSHOT_DIR")
        or os.environ.get("OUTLOOK_REGISTER_RESULTS_DIR")
        or tempfile.gettempdir()
    )
    os.makedirs(ss_dir, exist_ok=True)
    tmp_profile = tempfile.mkdtemp(prefix="outlook_oauth_")
    logger.info("[oauth] Starting OAuth for %s", email)

    try:
        import platform as _plat
        headless = "virtual" if _plat.system() == "Linux" else False
        with Camoufox(
            headless=headless, humanize=True, persistent_context=True,
            user_data_dir=tmp_profile, screen=Screen(max_width=1920, max_height=1080),
            proxy=cf_proxy, geoip=True, locale="zh-CN",
        ) as ctx:
            page = ctx.pages[0] if ctx.pages else ctx.new_page()
            captured_url = ""

            def on_request(request):
                nonlocal captured_url
                if redirect_url in request.url and ("code=" in request.url or "error=" in request.url):
                    captured_url = request.url

            page.on("request", on_request)
            try:
                page.goto(authorize_url, timeout=30000, wait_until="domcontentloaded")
                code, error = _complete_microsoft_oauth_page(
                    page,
                    email,
                    password,
                    redirect_url,
                    captured_url_fn=lambda: captured_url,
                )
            finally:
                try:
                    page.remove_listener("request", on_request)
                except Exception:
                    pass
            if not code:
                page.screenshot(path=os.path.join(ss_dir, "outlook_oauth_error.png"))
                result["error"] = error or "OAuth authorization code was not captured"
                logger.error("[oauth] %s", result["error"])
                return result

        token_resp = requests.post(
            "https://login.microsoftonline.com/common/oauth2/v2.0/token",
            data={
                "client_id": client_id,
                "grant_type": "authorization_code",
                "code": code,
                "redirect_uri": redirect_url,
                "code_verifier": verifier,
                "scope": " ".join(scopes),
            },
            timeout=30,
        )
        if token_resp.status_code != 200:
            result["error"] = f"token exchange failed status={token_resp.status_code}: {token_resp.text[:500]}"
            logger.error("[oauth] %s", result["error"])
            return result

        tokens = token_resp.json()
        refresh_token = str(tokens.get("refresh_token", "")).strip()
        if not refresh_token:
            result["error"] = "token exchange succeeded but no refresh_token was returned"
            logger.error("[oauth] %s", result["error"])
            return result

        result["success"] = True
        result["refresh_token"] = refresh_token
        result["access_token"] = str(tokens.get("access_token", "")).strip()
        logger.info("[oauth] OAuth successful for %s", email)
        return result
    except Exception as exc:
        result["error"] = str(exc)
        logger.exception("[oauth] OAuth failed")
        return result
    finally:
        shutil.rmtree(tmp_profile, ignore_errors=True)


# ---------------------------------------------------------------------------
# CAPTCHA handler (auto press-and-hold only)
# ---------------------------------------------------------------------------

def _handle_captcha(page, max_retries=3) -> bool:
    """Handle CAPTCHA, searching across all frames."""

    def _find_in_any_frame(selectors, timeout=5000):
        """Search for a visible locator across page and all frames."""
        deadline = time.time() + timeout / 1000
        while time.time() < deadline:
            for frame in page.frames:
                for sel in selectors:
                    try:
                        loc = frame.locator(sel)
                        if loc.count() > 0:
                            box = loc.first.bounding_box(timeout=500)
                            if box and box['width'] > 0 and box['height'] > 0:
                                return loc.first
                    except Exception:
                        pass
            page.wait_for_timeout(300)
        return None

    accessible_sel = ['[aria-label="可访问性挑战"]', '[aria-label="Accessible challenge"]']
    press_sel = ['[aria-label="再次按下"]', '[aria-label*="按住"]',
                 '[aria-label*="Press and hold"]', '[aria-label*="Press"]']

    for attempt in range(max_retries + 1):
        page.wait_for_timeout(random.randint(200, 500))

        # Step 1: Click accessible challenge button
        logger.info("[captcha] Looking for accessible challenge (attempt %d/%d)...", attempt + 1, max_retries)
        acc_btn = _find_in_any_frame(accessible_sel, timeout=10000)
        if acc_btn:
            try:
                _click_with_bezier(page, acc_btn, hold_ms=0)
                logger.info("[captcha] Clicked accessible challenge")
            except Exception as e:
                logger.warning("[captcha] Click accessible failed: %s", e)
        else:
            logger.warning("[captcha] No accessible challenge button found")

        # Step 2: Click the action button
        page.wait_for_timeout(random.randint(300, 600))
        logger.info("[captcha] Looking for action button...")
        press_btn = _find_in_any_frame(press_sel, timeout=10000)
        if not press_btn:
            logger.error("[captcha] No action button found")
            return False

        try:
            _click_with_bezier(page, press_btn, hold_ms=0)
            logger.info("[captcha] Action button clicked")
        except Exception as e:
            logger.error("[captcha] Action button click failed: %s", e)
            return False

        # Step 3: Wait for draw canvas to detach
        try:
            logger.info("[captcha] Waiting for challenge to resolve (.draw detach)...")
            page.locator('.draw').wait_for(state="detached", timeout=15000)
            logger.info("[captcha] Challenge resolved")
        except Exception:
            if page.get_by_text('取消').count() > 0:
                logger.info("[captcha] passed (cancel button)")
                return True
            logger.info("[captcha] .draw detach timeout, continuing to next attempt")
            continue

        # Step 4: Check result
        try:
            logger.info("[captcha] Checking result, waiting for loading indicator...")
            page.locator('[role="status"][aria-label="正在加载..."]').wait_for(timeout=5000)
            page.wait_for_timeout(8000)

            if page.get_by_text('一些异常活动').count() > 0 or page.get_by_text('此站点正在维护').count() > 0:
                logger.error("[captcha] Rate limited")
                return False

            # Check if challenge button reappeared (need retry)
            logger.info("[captcha] Checking if challenge reappeared...")
            retry_btn = _find_in_any_frame(accessible_sel, timeout=3000)
            if retry_btn:
                logger.info("[captcha] Need retry, attempt %d/%d", attempt + 1, max_retries)
                continue

            logger.info("[captcha] passed on attempt %d", attempt + 1)
            return True

        except Exception:
            if page.get_by_text('取消').count() > 0:
                logger.info("[captcha] passed (cancel button)")
                return True
            # Check for "请再试一次" in any frame
            logger.info("[captcha] Checking for retry text in frames...")
            for frame in page.frames:
                try:
                    if frame.get_by_text("请再试一次").count() > 0:
                        logger.info("[captcha] Retry requested")
                        break
                except Exception:
                    pass
            logger.info("[captcha] End of attempt %d, continuing...", attempt + 1)
            continue

    logger.error("[captcha] all attempts exhausted")
    return False


# ---------------------------------------------------------------------------
# Main registration flow (mirrors OutlookRegister base_controller logic)
# ---------------------------------------------------------------------------

def outlook_register(
    proxy: str = "",
    email_suffix: str = "@outlook.com",
    max_captcha_retries: int = 3,
    should_cancel_fn: Optional[Callable[[], bool]] = None,
) -> dict:
    from camoufox.sync_api import Camoufox
    from browserforge.fingerprints import Screen

    email_local = _gen_email_local()
    full_email = email_local + email_suffix
    password = _gen_password()
    first_name, last_name = _gen_name()
    year = str(random.randint(1960, 2005))
    month = str(random.randint(1, 12))
    day = str(random.randint(1, 28))

    wait_ms = BOT_PROTECTION_WAIT * 1000

    result = {"success": False, "email": full_email, "password": password, "error": ""}

    cf_proxy = None
    if proxy:
        pp = urlparse(proxy)
        cf_proxy = {"server": f"{pp.scheme}://{pp.hostname}:{pp.port}",
                     "username": pp.username or "", "password": pp.password or ""}

    tmp_profile = tempfile.mkdtemp(prefix="outlook_reg_")
    logger.info("[outlook] Starting: %s", full_email)

    ss_dir = (
        os.environ.get("SCREENSHOT_DIR")
        or os.environ.get("OUTLOOK_REGISTER_RESULTS_DIR")
        or tempfile.gettempdir()
    )
    os.makedirs(ss_dir, exist_ok=True)

    try:
        import platform as _plat
        headless = "virtual" if _plat.system() == "Linux" else False

        with Camoufox(
            headless=headless, humanize=True, persistent_context=True,
            user_data_dir=tmp_profile, screen=Screen(max_width=1920, max_height=1080),
            proxy=cf_proxy, geoip=True, locale="zh-CN",
        ) as ctx:
            page = ctx.pages[0] if ctx.pages else ctx.new_page()

            # [1] Open Outlook signup via prompt=create_account
            logger.info("[outlook] Opening signup...")
            try:
                page.goto("https://outlook.live.com/mail/0/?prompt=create_account",
                          timeout=20000, wait_until="domcontentloaded")
                page.get_by_text('同意并继续').wait_for(timeout=30000)
                start_time = time.time()
                page.wait_for_timeout(int(0.1 * wait_ms))
                page.get_by_text('同意并继续').click(timeout=30000)
                logger.info("[outlook] Consent accepted")
            except Exception as e:
                page.screenshot(path=os.path.join(ss_dir, "outlook_no_consent.png"))
                result["error"] = f"IP quality issue, cannot enter signup: {e}"
                logger.error("[outlook] %s", result["error"])
                return result

            # [2] Fill email
            logger.info("[outlook] Filling email: %s", full_email)
            try:
                # Switch domain if hotmail
                if email_suffix == "@hotmail.com":
                    page.get_by_text("@outlook.com").click(timeout=10000)
                    page.locator('[role="option"]:text-is("@hotmail.com")').click()

                page.locator('[aria-label="新建电子邮件"]').type(
                    email_local, delay=int(0.006 * wait_ms), timeout=10000)
                page.locator('[data-testid="primaryButton"]').click(timeout=5000)
                page.wait_for_timeout(int(0.02 * wait_ms))
            except Exception as e:
                page.screenshot(path=os.path.join(ss_dir, "outlook_email_error.png"))
                result["error"] = f"Email fill failed: {e}"
                logger.error("[outlook] %s", result["error"])
                return result

            # [3] Fill password
            logger.info("[outlook] Filling password...")
            try:
                page.locator('[type="password"]').type(
                    password, delay=int(0.004 * wait_ms), timeout=10000)
                page.wait_for_timeout(int(0.02 * wait_ms))
                page.locator('[data-testid="primaryButton"]').click(timeout=5000)
                page.wait_for_timeout(int(0.03 * wait_ms))
            except Exception as e:
                page.screenshot(path=os.path.join(ss_dir, "outlook_pwd_error.png"))
                result["error"] = f"Password fill failed: {e}"
                logger.error("[outlook] %s", result["error"])
                return result

            # [4] Fill birthday + name (same page)
            logger.info("[outlook] Filling birthday & name...")
            try:
                year_input = _first_visible_locator(page, [
                    '[name="BirthYear"]',
                    '#BirthYear',
                    'input[aria-label="年份"]',
                    'input[aria-label*="Year"]',
                    'input[placeholder*="年"]',
                    'input[inputmode="numeric"]',
                    'input[type="number"]',
                ], timeout=10000)
                _type_into(year_input, year, delay=int(0.001 * wait_ms))
                page.wait_for_timeout(int(0.02 * wait_ms))

                month_names = [
                    "January", "February", "March", "April", "May", "June",
                    "July", "August", "September", "October", "November", "December",
                ]
                try:
                    _select_birth_value(
                        page,
                        "BirthMonth",
                        month,
                        [f"{month}月", month_names[int(month) - 1], str(int(month))],
                    )
                    page.wait_for_timeout(int(0.04 * wait_ms))
                    _select_birth_value(
                        page,
                        "BirthDay",
                        day,
                        [f"{day}日", str(int(day))],
                    )
                except Exception:
                    page.locator('[name="BirthMonth"]').click()
                    page.wait_for_timeout(int(0.02 * wait_ms))
                    page.locator(f'[role="option"]:text-is("{month}月")').click()
                    page.wait_for_timeout(int(0.04 * wait_ms))
                    page.locator('[name="BirthDay"]').click()
                    page.wait_for_timeout(int(0.03 * wait_ms))
                    page.locator(f'[role="option"]:text-is("{day}日")').click()
                _click_primary(page, timeout=5000)
                page.wait_for_timeout(int(0.03 * wait_ms))

                last_name_input = _first_visible_locator(page, [
                    '#lastNameInput',
                    '[name="LastName"]',
                    '[name="lastName"]',
                    'input[aria-label="姓"]',
                    'input[aria-label*="Last"]',
                    'input[aria-label*="Surname"]',
                    'input[placeholder="姓"]',
                ], timeout=15000)
                _type_into(last_name_input, last_name, delay=int(0.002 * wait_ms))
                page.wait_for_timeout(int(0.02 * wait_ms))

                first_name_input = _first_visible_locator(page, [
                    '#firstNameInput',
                    '[name="FirstName"]',
                    '[name="firstName"]',
                    'input[aria-label="名"]',
                    'input[aria-label*="First"]',
                    'input[aria-label*="Given"]',
                    'input[placeholder="名"]',
                ], timeout=10000)
                _type_into(first_name_input, first_name)

                # Wait for bot protection time
                elapsed = time.time() - start_time
                if elapsed < BOT_PROTECTION_WAIT:
                    remaining_ms = int((BOT_PROTECTION_WAIT - elapsed) * 1000)
                    logger.info("[outlook] Waiting %dms for bot protection timer...", remaining_ms)
                    page.wait_for_timeout(remaining_ms)

                _click_primary(page, timeout=5000)
                # Wait for privacy link to detach (page transition)
                page.locator('span > [href="https://go.microsoft.com/fwlink/?LinkID=521839"]').wait_for(
                    state='detached', timeout=22000)
                page.wait_for_timeout(400)
            except Exception as e:
                page.screenshot(path=os.path.join(ss_dir, "outlook_form_error.png"))
                result["error"] = f"Form fill failed: {e}"
                logger.error("[outlook] %s", result["error"])
                return result

            # [5] Check for rate limit before captcha
            if page.get_by_text('一些异常活动').count() > 0 or \
               page.get_by_text('此站点正在维护').count() > 0:
                page.screenshot(path=os.path.join(ss_dir, "outlook_rate_limit.png"))
                result["error"] = "Rate limited (IP flagged)"
                logger.error("[outlook] %s", result["error"])
                return result

            if page.locator('iframe#enforcementFrame').count() > 0:
                page.screenshot(path=os.path.join(ss_dir, "outlook_funcaptcha.png"))
                result["error"] = "FunCaptcha type detected (not press-and-hold)"
                logger.error("[outlook] %s", result["error"])
                return result

            # [6] Handle CAPTCHA
            logger.info("[outlook] Handling CAPTCHA...")
            captcha_ok = _handle_captcha(page, max_retries=max_captcha_retries)
            if not captcha_ok:
                page.screenshot(path=os.path.join(ss_dir, "outlook_captcha_fail.png"))
                result["error"] = "CAPTCHA failed"
                logger.error("[outlook] %s", result["error"])
                return result

            # [7] Success!
            result["success"] = True
            result["email"] = full_email
            logger.info("[outlook] ✅ Registration successful: %s", full_email)

    except Exception as e:
        result["error"] = str(e)
        logger.exception("[outlook] Registration failed")
    finally:
        try:
            shutil.rmtree(tmp_profile, ignore_errors=True)
        except Exception:
            pass

    return result


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    import argparse
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s", datefmt="%H:%M:%S")

    parser = argparse.ArgumentParser(description="Camoufox Outlook Registration")
    parser.add_argument("--proxy", default=os.environ.get("OUTLOOK_REGISTER_PROXY", ""), help="Proxy URL")
    parser.add_argument("--suffix", default=os.environ.get("OUTLOOK_REGISTER_EMAIL_SUFFIX", "@outlook.com"), help="Email suffix")
    parser.add_argument("--max-retries", type=int, default=int(os.environ.get("OUTLOOK_REGISTER_MAX_CAPTCHA_RETRIES", "3")), help="Max CAPTCHA retries")
    parser.add_argument("--results-dir", default=os.environ.get("OUTLOOK_REGISTER_RESULTS_DIR", ""), help="Directory to output results")
    args = parser.parse_args()

    result = outlook_register(proxy=args.proxy, email_suffix=args.suffix,
                              max_captcha_retries=args.max_retries)

    print("\n" + "=" * 50)
    if result["success"]:
        print(f"✅ Registration successful!")
        print(f"   Email:    {result['email']}")
        print(f"   Password: {result['password']}")
        if args.results_dir:
            os.makedirs(args.results_dir, exist_ok=True)
            out_file = os.path.join(args.results_dir, "unlogged_email.txt")
            try:
                with open(out_file, "a", encoding="utf-8") as f:
                    f.write(f"{result['email']}:{result['password']}\n")
                logger.info(f"[outlook] Saved result to {out_file}")
            except Exception as e:
                logger.error(f"[outlook] Failed to save result: {e}")
    else:
        print(f"❌ Registration failed: {result['error']}")
        print(f"   Email:    {result['email']}")
    print("=" * 50)


if __name__ == "__main__":
    main()
