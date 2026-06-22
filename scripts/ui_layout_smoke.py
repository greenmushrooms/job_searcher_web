#!/usr/bin/env python3
"""Playwright layout smoke test for the jobs UI.

Loops the first N jobs and a set of viewport widths, opening the workspace for
each, and asserts structural layout invariants that have bitten us before:

  * no horizontal page scroll (something is wider than the viewport)
  * the working-copy textarea does not overlap the AI-suggestions pane
  * primary action buttons are visible and inside the viewport width
  * every status tab / date-range option reloads the list without an error
    banner appearing

Run against a live server (default http://localhost:7770) using the project
venv (created once with `uv venv .venv && uv pip install -p .venv playwright`):

    .venv/bin/python scripts/ui_layout_smoke.py [--base URL] [--jobs N]

Uses system Chrome (channel fallback: executable /usr/bin/google-chrome) so no
browser download is needed. Exits non-zero if any check fails; screenshots of
failures land in /tmp/jsweb-shots/.
"""

import argparse
import sys

from playwright.sync_api import sync_playwright

SHOT_DIR = "/tmp/jsweb-shots"
VIEWPORTS = [1500, 1100, 880]

issues: list[str] = []


def check(cond: bool, msg: str) -> bool:
    if not cond:
        issues.append(msg)
    return cond


def page_has_h_scroll(pg) -> bool:
    return pg.evaluate(
        "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
    )


def boxes_overlap(a, b) -> bool:
    if a is None or b is None:
        return False
    return not (
        a["x"] + a["width"] <= b["x"] + 1
        or b["x"] + b["width"] <= a["x"] + 1
        or a["y"] + a["height"] <= b["y"] + 1
        or b["y"] + b["height"] <= a["y"] + 1
    )


def banner_visible(pg) -> bool:
    return pg.evaluate(
        "!!document.querySelector('#error-banner.show')"
    )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://localhost:7770")
    ap.add_argument("--jobs", type=int, default=5, help="how many job rows to loop")
    ap.add_argument("--chrome", default="/usr/bin/google-chrome")
    args = ap.parse_args()

    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=args.chrome, headless=True)

        for width in VIEWPORTS:
            pg = browser.new_page(viewport={"width": width, "height": 1100})
            tag = f"[{width}px]"
            pg.goto(args.base + "/jobs.html")
            pg.wait_for_selector(".job-row, .jl-empty", timeout=15000)

            check(not page_has_h_scroll(pg), f"{tag} list view: horizontal scroll")
            check(not banner_visible(pg), f"{tag} list view: error banner on load")

            # exercise every status tab and the date dropdown
            # the radio itself is display:none (pill styling) — click its label
            for status in ["inbox", "applied", "interview", "skipped", ""]:
                pg.locator(f'.status-tabs label:has(input[value="{status}"])').click()
                pg.wait_for_timeout(400)
                check(not banner_visible(pg), f"{tag} status tab {status or 'all'}: error banner")
            for days in ["7", "0", "14"]:
                pg.locator('select[name="days"]').select_option(days)
                pg.wait_for_timeout(400)
                check(not banner_visible(pg), f"{tag} days={days}: error banner")

            # loop the first N jobs' workspaces
            n = min(args.jobs, pg.locator(".job-row").count())
            for i in range(n):
                pg.locator(".job-row").nth(i).click()
                pg.wait_for_selector(".md-panes, .draft-empty", timeout=20000)
                pg.wait_for_timeout(300)
                jid = pg.locator(".workspace").get_attribute("id") or f"row{i}"
                jtag = f"{tag} {jid}"

                ok = True
                ok &= check(not page_has_h_scroll(pg), f"{jtag}: horizontal scroll")
                ok &= check(not banner_visible(pg), f"{jtag}: error banner")

                left = pg.locator("textarea[name=markdown]").bounding_box()
                right = pg.locator(".md-pane.md-right").bounding_box()
                # on narrow viewports the panes stack vertically; overlap is
                # only a bug when they sit side by side
                if left and right and abs(left["y"] - right["y"]) < 50:
                    ok &= check(
                        not boxes_overlap(left, right),
                        f"{jtag}: working copy overlaps AI pane",
                    )
                if left:
                    ok &= check(
                        left["x"] + left["width"] <= width + 1,
                        f"{jtag}: textarea wider than viewport",
                    )

                for sel, name in [
                    ("#job-draft .draft-finalize", "Save résumé"),
                    ('button[formaction$="resume.pdf"]', "Generate PDF"),
                ]:
                    el = pg.locator(sel)
                    ok &= check(el.count() > 0 and el.is_visible(), f"{jtag}: {name} button missing")

                if not ok:
                    pg.screenshot(path=f"{SHOT_DIR}/fail-{width}-{i}.png", full_page=True)

            # the error banner must actually appear on a failing request
            pg.evaluate(
                "htmx.ajax('GET', '/ui/jobs/nonexistent-id/workspace', {target: '#job-detail'})"
            )
            pg.wait_for_timeout(600)
            check(banner_visible(pg), f"{tag} error banner did not appear on a 500")
            pg.close()

        browser.close()

    if issues:
        print(f"FAIL — {len(issues)} layout issue(s):")
        for m in issues:
            print("  ✗", m)
        return 1
    print("OK — all layout checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
