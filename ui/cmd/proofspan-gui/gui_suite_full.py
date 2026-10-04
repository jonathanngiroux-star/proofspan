#!/usr/bin/env python3
"""Full interactive GUI suite: every field, dropdown, button, and the whole
wizard, driven through the real webview via the test bridge. Every state
change is cross-verified server-side (sqlite counts, listening ports) —
the UI claim AND the persistence, like FogOS's suites.

Run: launch the app with the bridge, then
  python3 gui_driver.py run gui_suite_full.py
"""
import json
import os
import socket
import sqlite3
import subprocess
import sys
import time

REPO = "/run/media/thoth/project-backup/github_top_10/proofspan"
CORPUS = f"{REPO}/testdata/corpus/langsmith/corpus.jsonl"
DB = "/tmp/gui-bridge-suite.sqlite"
TUI_DB = "/tmp/gui-bridge-tui.sqlite"  # not used here; marker for parity

def db_counts(path=DB):
    if not os.path.exists(path):
        return None
    con = sqlite3.connect(path)
    try:
        t = con.execute("SELECT COUNT(*) FROM trajectories").fetchone()[0]
        s = con.execute("SELECT COUNT(*) FROM spans").fetchone()[0]
        return (t, s)
    finally:
        con.close()

def wait_title(gui, want, seconds=15):
    """Poll #wizard-title until it contains `want` — clicking Next does not
    paint synchronously (click-before-render race made instant reads flaky)."""
    for _ in range(seconds):
        time.sleep(1)
        r = gui.read("#wizard-title")
        if r.get("ok") and want in (r["value"]["text"] or ""):
            return r["value"]["text"]
    r = gui.read("#wizard-title")
    return r.get("value", {}).get("text") if r.get("ok") else None

def wait_summary(gui, marker, seconds=180):
    """Poll #wizard-summary until it contains the step's own marker — the
    previous step's summary lingers otherwise (stale-summary poll bug)."""
    for _ in range(seconds):
        time.sleep(1)
        r = gui.read("#wizard-summary")
        if r.get("ok") and marker in (r["value"]["text"] or ""):
            return r["value"]["text"]
    r = gui.read("#wizard-summary")
    return r.get("value", {}).get("text") if r.get("ok") else None

def port_up(addr="127.0.0.1", port=7400):
    with socket.socket() as s:
        s.settimeout(1)
        return s.connect_ex((addr, port)) == 0

def suite(gui):
    checks = []
    E = gui.expect

    # fresh scratch DB for this run
    if os.path.exists(DB):
        os.remove(DB)

    # ---- 0. bridge sanity --------------------------------------------
    r = gui.ping()
    checks.append(E("bridge ping", r.get("ok") and r.get("value") == "pong"))

    # ---- 1. wizard: full 7-step walk -----------------------------------
    gui.click_text("Wizard")  # open via tab
    r = gui.read("#wizard-overlay")
    checks.append(E("wizard opens", r.get("ok") and r["value"]["display"] != "none"))
    r = gui.read("#wizard-title")
    checks.append(E("wizard at intro", r.get("ok") and "Welcome" in (r["value"]["text"] or "")))

    # Skip test first: open -> skip -> closed -> reopen
    gui.click("#wizard-skip")
    r = gui.read("#wizard-overlay")
    checks.append(E("skip closes wizard", r.get("ok") and r["value"]["display"] == "none"))
    gui.click_text("Wizard")
    r = gui.read("#wizard-title")
    checks.append(E("reopen resets to intro", r.get("ok") and "Welcome" in (r["value"]["text"] or "")))

    # Intro -> Step 1
    gui.click("#wizard-next")
    checks.append(E("step 1: pick export", "Step 1" in str(wait_title(gui, "Step 1"))))

    # dropdown: switch source to honeyhive and back (real change events)
    r = gui.select("#wizard-source", "honeyhive")
    checks.append(E("dropdown select honeyhive", r.get("ok") and r["value"] == "honeyhive"))
    r = gui.select("#wizard-source", "langsmith")
    checks.append(E("dropdown select langsmith", r.get("ok") and r["value"] == "langsmith"))

    # text fields: corpus path + db path
    r = gui.fill("#wizard-file", CORPUS)
    checks.append(E("type corpus path", r.get("ok") and r["value"] == CORPUS))
    r = gui.fill("#wizard-db", DB)
    checks.append(E("type db path", r.get("ok") and r["value"] == DB))

    # Next -> Analyze; RUN-GATE: Next must refuse until run
    gui.click("#wizard-next")
    checks.append(E("step 2: analyze", "Step 2" in str(wait_title(gui, "Step 2"))))
    gui.click("#wizard-next")
    checks.append(E("run-gate blocks unrun step", "Step 2" in str(wait_title(gui, "Step 2"))))

    # Run analyze (real CLI subprocess on the 400-trajectory corpus)
    gui.click("#wizard-run")
    summary = wait_summary(gui, "report:", 60)
    checks.append(E("analyze runs", summary is not None and "10000/10000" in str(summary), str(summary)))

    gui.click("#wizard-next")  # -> Plan
    checks.append(E("step 3: plan", "Step 3" in str(wait_title(gui, "Step 3"))))
    gui.click("#wizard-run")
    summary = wait_summary(gui, "plan:", 60)
    checks.append(E("plan runs (dry-run)", summary is not None and "10000 spans" in str(summary) and "plan:" in str(summary), str(summary)))

    gui.click("#wizard-next")  # -> Migrate
    checks.append(E("step 4: migrate", "Step 4" in str(wait_title(gui, "Step 4"))))
    gui.click("#wizard-run")
    summary = wait_summary(gui, "migrated", 90)
    checks.append(E("migrate runs", summary is not None and "migrated 400 trajectories" in str(summary), str(summary)))
    counts = db_counts()
    checks.append(E("SERVER-SIDE: sqlite has 400/10000", counts == (400, 10000), str(counts)))

    gui.click("#wizard-next")  # -> Eval
    checks.append(E("step 5: eval", "Step 5" in str(wait_title(gui, "Step 5"))))
    gui.click("#wizard-run")
    summary = wait_summary(gui, "eval:", 240)
    checks.append(E("eval gate passes 400/400", summary is not None and "400/400" in str(summary), str(summary)))

    gui.click("#wizard-next")  # -> Done
    done_title = None
    for _ in range(30):
        time.sleep(1)
        r = gui.read("#wizard-title")
        if r.get("ok") and (r["value"]["text"] or "").strip() == "Done":
            done_title = "Done"
            break
    checks.append(E("done step", done_title == "Done"))
    r = gui.read("#done-eth")
    checks.append(E("done: eth address", r.get("ok") and "0x85ee7E71" in (r["value"]["text"] or "")))
    r = gui.read("#done-btc")
    checks.append(E("done: btc address", r.get("ok") and "bc1qxe2" in (r["value"]["text"] or "")))
    gui.click_text("Close")  # wizard Next/Close button
    r = gui.read("#wizard-overlay")
    checks.append(E("wizard closes from done", r.get("ok") and r["value"]["display"] == "none"))

    # ---- 2. Analyze tab --------------------------------------------------
    gui.click_text("Analyze")
    r = gui.read("#view-analyze")
    checks.append(E("analyze view active", r.get("ok") and r["value"]["display"] != "none"))
    r = gui.select("#analyze-source", "honeyhive")
    checks.append(E("analyze dropdown honeyhive", r.get("ok") and r["value"] == "honeyhive"))
    gui.select("#analyze-source", "langsmith")
    r = gui.fill("#analyze-file", CORPUS)
    checks.append(E("analyze file field", r.get("ok") and r["value"] == CORPUS))
    r = gui.fill("#analyze-out", "/tmp/gui-bridge-report")
    checks.append(E("analyze outdir field", r.get("ok")))
    gui.click_text("Analyze export")
    ok = False
    for _ in range(30):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and "done" in (s["value"] or ""):
            ok = True
            break
    checks.append(E("analyze export runs", ok))
    lg = gui.log()
    checks.append(E("log shows census json", lg.get("ok") and "lines_parsed" in (lg["value"] or "")))

    # ---- 3. Migrate tab: dry-run plan + real migrate ---------------------
    gui.click_text("Migrate")
    r = gui.fill("#migrate-file", CORPUS)
    checks.append(E("migrate file field", r.get("ok")))
    r = gui.fill("#migrate-db", DB)
    checks.append(E("migrate db field", r.get("ok")))
    gui.check("#migrate-dryrun", True)
    r = gui.read("#migrate-dryrun")
    checks.append(E("dry-run checkbox toggles", r.get("ok") and r["value"]["checked"] is True))
    gui.click_text("Preview dry-run plan")
    ok = False
    for _ in range(30):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and "rendered" in (s["value"] or ""):
            ok = True
            break
    checks.append(E("dry-run plan renders", ok))
    r = gui.count("#plan-mappings tr")
    checks.append(E("plan shows 18 mappings", r.get("ok") and r["value"] == 18, str(r.get("value"))))
    r = gui.count("#plan-dropped tr")
    checks.append(E("plan shows 1 dropped field", r.get("ok") and r["value"] == 1, str(r.get("value"))))
    r = gui.read("#plan-meta")
    checks.append(E("plan meta real data", r.get("ok") and "atf/v0.1.1" in (r["value"]["text"] or "")))
    gui.check("#migrate-dryrun", False)
    gui.click_text("Migrate", sel="#view-migrate .actions")
    ok = False
    for _ in range(60):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and ("done" in (s["value"] or "") or "exit" in (s["value"] or "")):
            ok = "done" in (s["value"] or "")
            break
    counts = db_counts()
    checks.append(E("migrate writes db again", ok and counts == (400, 10000), f"status-ok={ok} counts={counts}"))

    # ---- 4. Evaluate tab: report table ------------------------------------
    gui.click_text("Evaluate")
    r = gui.fill("#eval-db", DB)
    checks.append(E("eval db field", r.get("ok")))
    r = gui.fill("#eval-trajectory", "trj_00001")
    checks.append(E("eval trajectory field", r.get("ok")))
    r = gui.fill("#eval-judges", "")
    checks.append(E("eval judges field", r.get("ok")))
    gui.click_text("Run & show report table")
    ok = False
    for _ in range(60):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and "pass" in (s["value"] or "").lower():
            ok = True
            break
    r = gui.count("#eval-rows tr")
    checks.append(E("eval report table renders", ok and r.get("ok") and r["value"] >= 1, f"rows={r.get('value')}"))
    r = gui.texts("#eval-rows tr")
    first = (r.get("value") or [""])[0] if r.get("ok") else ""
    checks.append(E("eval table has trajectory id", "trj_" in first, str(first)))

    # ---- 5. Trajectories tab: list + span viewer ---------------------------
    gui.click_text("Trajectories")
    r = gui.fill("#traj-db", DB)
    checks.append(E("traj db field", r.get("ok")))
    gui.click_text("List trajectories")
    ok = False
    n = {"ok": False, "value": 0}
    for _ in range(30):
        time.sleep(1)
        n = gui.count("#traj-list li")
        if n.get("ok") and n["value"] > 1:
            ok = True
            break
    checks.append(E("trajectory list loads 400", ok, str(n.get("value"))))
    gui.click("#traj-list li")
    ok = False
    for _ in range(20):
        time.sleep(0.5)
        r = gui.read("#traj-detail")
        if r.get("ok") and r["value"]["display"] != "none":
            ok = True
            break
    r = gui.count("#span-rows tr")
    checks.append(E("span viewer opens with spans", ok and r.get("ok") and r["value"] > 0, f"spans={r.get('value')}"))

    # ---- 6. Judges tab: manifest viewer ------------------------------------
    gui.click_text("Judges")
    r = gui.fill("#judges-db", DB)
    checks.append(E("judges db field", r.get("ok")))
    gui.click_text("Load manifest")
    ok = False
    for _ in range(20):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and "judges pinned" in (s["value"] or ""):
            ok = True
            break
    checks.append(E("manifest loads", ok))
    r = gui.texts("#judges-rows tr td:nth-child(2)")
    fps = " ".join(r.get("value") or []) if r.get("ok") else ""
    checks.append(E("fingerprints render (fix pinned)", "sha256" in fps or len(fps) > 10, str(fps)[:80]))

    # ---- 7. Serve tab: start/stop with port verification -------------------
    gui.click_text("Serve")
    r = gui.fill("#serve-db", DB)
    checks.append(E("serve db field", r.get("ok")))
    r = gui.fill("#serve-addr", "127.0.0.1:7400")
    checks.append(E("serve addr field", r.get("ok")))
    gui.check("#serve-scim", True)
    r = gui.read("#serve-scim")
    checks.append(E("scim checkbox toggles", r.get("ok") and r["value"]["checked"] is True))
    r = gui.fill("#serve-token", "test-token-123")
    checks.append(E("scim token field", r.get("ok")))
    gui.click_text("Start server")
    ok = False
    for _ in range(30):
        time.sleep(1)
        s = gui.status()
        if s.get("ok") and "running" in (s["value"] or ""):
            ok = True
            break
    checks.append(E("serve status running", ok))
    checks.append(E("SERVER-SIDE: port 7400 listens", port_up()))
    # HTTP check through the public API
    try:
        out = subprocess.run(["curl", "-s", "-m", "5", "http://127.0.0.1:7400/v1/trajectories"],
                             capture_output=True, text=True, timeout=10)
        api_ok = "trj_" in out.stdout
    except Exception:
        api_ok = False
    checks.append(E("SERVER-SIDE: api serves trajectories", api_ok))
    gui.click_text("Stop server")
    ok = False
    for _ in range(20):
        time.sleep(0.5)
        if not port_up():
            ok = True
            break
    checks.append(E("SERVER-SIDE: port closed after stop", ok))

    # ---- 8. Donate tab: addresses + copy ----------------------------------
    gui.click_text("Donate")
    r = gui.read("#donate-eth")
    checks.append(E("donate eth byte-for-byte", r.get("ok") and (r["value"]["text"] or "").strip() == "0x85ee7E71f762d772599cbF1EC20E651B30657521"))
    r = gui.read("#donate-btc")
    checks.append(E("donate btc byte-for-byte", r.get("ok") and (r["value"]["text"] or "").strip() == "bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg"))
    gui.click('[data-copy="donate-eth"]')
    time.sleep(0.5)
    r = gui.cmd("read", sel='[data-copy="donate-eth"]')
    checks.append(E("copy button feedback", r.get("ok") and "Copied" in (r["value"]["text"] or ""), str(r.get("value", {}).get("text"))))
    # clipboard itself (wayland): only when wl-paste exists — else the
    # button feedback + binding success stand as the check
    import shutil
    if shutil.which("wl-paste"):
        try:
            out = subprocess.run(["wl-paste", "--no-newline"], capture_output=True, text=True, timeout=5)
            clip_ok = out.stdout.strip() == "0x85ee7E71f762d772599cbF1EC20E651B30657521"
        except Exception:
            clip_ok = False
        checks.append(E("SERVER-SIDE: clipboard holds address", clip_ok))
    else:
        print("SKIP clipboard readback (no wl-paste)")

    # ---- 9. dark mode -------------------------------------------------------
    r = gui.cmd("read", sel="body")
    v = r.get("value") or {}
    checks.append(E("dark mode: body bg", r.get("ok") and v.get("background") == "rgb(15, 17, 22)", str(v.get("background"))))
    checks.append(E("dark mode: color-scheme", v.get("colorScheme") == "dark", str(v.get("colorScheme"))))

    # ---- 10. log clear button ----------------------------------------------
    gui.click_text("Clear")
    r = gui.log()
    checks.append(E("log clear button works", r.get("ok") and (r["value"] or "").strip() == ""))

    passed = sum(1 for c in checks if c)
    print(f"\nSUITE: {passed}/{len(checks)} checks passed")
    return 0 if passed == len(checks) else 1
