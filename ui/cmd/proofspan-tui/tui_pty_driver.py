#!/usr/bin/env python3
"""TUI PTY driver: spawns the real proofspan-tui binary, drives it with
keystrokes over a pty, and asserts on the rendered output. This is the
terminal-side counterpart of the GUI's -selftest mode: the UI tests
itself, for real.

Usage: tui_pty_driver.py <tui-binary> <corpus.jsonl> [db.sqlite]
Exit 0 = all checks pass; 1 = failure (details on stderr).
"""
import os
import pty
import select
import sys
import threading
import time

KEY_ENTER = b"\r"
KEY_ESC = b"\x1b"
BACKSPACE = b"\x7f"

# donate addresses — byte-for-byte; OSC 52 payloads are their base64
import base64 as _b64
ETH = "0x85ee7E71f762d772599cbF1EC20E651B30657521"
BTC = "bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg"
ETH_B64 = _b64.b64encode(ETH.encode()).decode()
BTC_B64 = _b64.b64encode(BTC.encode()).decode()


class TUI:
    """Drives the real TUI binary over a pty.

    Plays the part of the terminal emulator too: termenv (via lipgloss)
    sends OSC 11 (background color query) and CSI 6n (cursor position) at
    startup and BLOCKS on the reply — a pty has no emulator to answer, so
    first paint would hang until a key arrives. We answer both queries
    like a dark-background xterm would, which unblocks rendering exactly
    as it is on a user's terminal.
    """

    def __init__(self, binary):
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.environ.setdefault("TERM", "xterm-256color")
            os.execv(binary, [binary])
            os._exit(127)
        self.screen = b""
        # Background reader: the pty's buffers are small and the TUI's
        # repaints during typing flood the output side; if nobody drains,
        # the tty layer stalls and DROPS input bytes (the db path's tail
        # vanished exactly this way). A dedicated thread keeps the pipe
        # empty and also answers any capability queries.
        self._stop = threading.Event()
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()
        # bubbletea needs a beat to paint the first frame
        time.sleep(0.7)
        self.drain()
        # bubbletea needs a beat to paint the first frame
        time.sleep(0.7)
        self.drain()

    def _read_loop(self):
        """Background: drain the pty output continuously and answer any
        capability queries (OSC 11 background, CSI 6n cursor) forever —
        termenv re-queries on repaints, not just at startup."""
        while not self._stop.is_set():
            try:
                r, _, _ = select.select([self.fd], [], [], 0.05)
                if not r:
                    continue
                chunk = os.read(self.fd, 65536)
                if not chunk:
                    break
                self.screen += chunk
                if len(self.screen) > 4_000_000:
                    self.screen = self.screen[-2_000_000:]
                if b"\x1b]11;?" in chunk:
                    os.write(self.fd, b"\x1b]11;rgb:0f11/1722/0f11\x1b\\")
                if b"\x1b[6n" in chunk:
                    os.write(self.fd, b"\x1b[1;1R")
            except OSError:
                break

    def drain(self, wait=0.3):
        # the background reader keeps the pipe empty; drain just waits so
        # callers can settle before asserting on screen state.
        deadline = time.time() + wait
        while time.time() < deadline:
            time.sleep(0.05)

    def send(self, keys, settle=0.35, human=False):
        # bubbletea's input reader coalesces multi-char writes into bulk
        # input that the key handler never sees as keystrokes. Printable
        # strings must be typed ONE character per write with a real gap —
        # verified empirically: single chars at 0.15s land, chunks don't.
        if human:
            # one char per write, real gaps, NO micro-drain during typing:
            # interleaved drain-reads during typing caused the reader to
            # misframe and lose the string's tail characters.
            for ch in keys:
                try:
                    os.write(self.fd, bytes([ch]))
                except OSError:
                    break
                time.sleep(0.15)
        else:
            try:
                os.write(self.fd, keys)
            except OSError:
                pass
        time.sleep(settle)
        self.drain()

    def expect(self, needle, timeout=10, what="", raw=False):
        """Wait until the needle appears in the raw stream (the buffer
        keeps history — the alt-screen repaints are all in there).
        raw=True searches the UNSTRIPPED bytes — required for escape-
        sequence assertions (OSC 52 copy payloads are stripped by plain())."""
        deadline = time.time() + timeout
        hay = self.screen if raw else self.plain()
        while time.time() < deadline:
            if needle.encode() in hay:
                return True
            self.drain(0.4)
            hay = self.screen if raw else self.plain()
        sys.stderr.write(f"FAIL: {what or needle!r} not on screen within {timeout}s\n")
        sys.stderr.write("--- screen tail ---\n")
        sys.stderr.write(self.plain()[-2500:].decode(errors="replace") + "\n")
        return False

    def plain(self):
        import re
        # strip ANSI escape sequences for robust matching
        return re.sub(rb"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][A-B0-9]|\r", b"", self.screen)

    def quit(self):
        self._stop.set()
        try:
            self._reader.join(timeout=1.0)
        except Exception:
            pass
        try:
            os.write(self.fd, b"q")
            time.sleep(0.4)
        except OSError:
            pass
        try:
            os.close(self.fd)
        except OSError:
            pass
        try:
            os.waitpid(self.pid, 0)
        except ChildProcessError:
            pass


def main():
    if len(sys.argv) < 3:
        sys.stderr.write(__doc__)
        return 2
    binary, corpus = sys.argv[1], sys.argv[2]
    db = sys.argv[3] if len(sys.argv) > 3 else "/tmp/proofspan-tui-wizard.sqlite"
    if os.path.exists(db):
        os.remove(db)

    checks = []

    t = TUI(binary)

    # 1. the app renders with its tab bar
    checks.append(("tab bar renders", t.expect("Analyze", what="tab bar")))
    checks.append(("help mentions wizard", t.expect("w wizard", what="help line")))

    # 2. open the wizard with 'w'
    t.send(b"w")
    checks.append(("wizard opens", t.expect("Welcome to Proofspan", what="wizard intro")))

    # 3. next → source page asks for the export; wait for ITS text
    t.send(b"n", settle=0.6)
    checks.append(("source page shows", t.expect("Pick your export", what="source step")))

    # 4. inline input on the source page: enter commits source → type file
    #    → enter → clear db default → type ours → enter. Whole strings in
    #    one write (per-char writes starved the reader and dropped keys).
    t.send(b"i", settle=0.6)
    checks.append(("input mode opens", t.expect("input >", what="input mode")))
    t.send(KEY_ENTER, settle=0.5)
    checks.append(("input advances to file", t.expect("export file path", what="file field prompt")))
    t.send(corpus.encode(), settle=1.0, human=True)
    t.send(KEY_ENTER, settle=1.0)
    checks.append(("input advances to db", t.expect("database path", what="db field prompt")))
    for _ in range(len("proofspan.sqlite")):
        t.send(BACKSPACE, settle=0.15)
    t.send(db.encode(), settle=1.0, human=True)
    t.send(KEY_ENTER, settle=1.2)
    checks.append(("input mode exits after db", t.expect("inputs set", what="post-input status")))

    # 5. next → analyze; wait for the analyze page's own text
    t.send(b"n", settle=0.8)
    checks.append(("analyze step shows", t.expect("Step 2", what="analyze step")))
    t.send(b"r")
    checks.append(("analyze runs", t.expect("lines parsed", timeout=25, what="analyze summary")))
    t.send(b"n", settle=0.8)

    # 6. plan step: dry-run, real counts
    checks.append(("plan step shows", t.expect("Step 3", what="plan step")))
    t.send(b"r", settle=1.0)
    checks.append(("plan runs", t.expect("400 trajectories", timeout=30, what="plan counts")))
    t.send(b"n", settle=0.8)

    # 7. migrate: writes the db
    checks.append(("migrate step shows", t.expect("Step 4", what="migrate step")))
    t.send(b"r", settle=1.0)
    checks.append(("migrate runs", t.expect("migrated 400 trajectories", timeout=30, what="migrate summary")))
    t.send(b"n", settle=0.8)

    # 8. eval: the gate
    checks.append(("eval step shows", t.expect("Step 5", what="eval step")))
    t.send(b"r", settle=1.0)
    checks.append(("eval runs", t.expect("trajectories pass", timeout=40, what="eval verdict")))
    t.send(b"n", settle=0.8)
    checks.append(("done page shows", t.expect("wizard step 7/7", what="done page")))

    # 9. close the wizard, confirm tabs still work
    t.send(b"w")
    checks.append(("wizard closes", t.expect("tabs as before", what="post-wizard status")))

    # 9b. donate tab: addresses byte-for-byte + OSC 52 copy keys
    t.send(b"5")
    checks.append(("donate tab in bar", t.expect("5 Donate", what="donate tab label")))
    checks.append(("eth address shown", t.expect(ETH, what="ETH address")))
    checks.append(("btc address shown", t.expect(BTC, what="BTC address")))
    checks.append(("copy hints shown", t.expect("c copy ETH", what="copy hint")))
    t.send(b"c")
    checks.append(("osc52 eth emitted", t.expect("52;c;" + ETH_B64, what="OSC 52 ETH payload", raw=True)))
    t.send(b"C")
    checks.append(("osc52 btc emitted", t.expect("52;c;" + BTC_B64, what="OSC 52 BTC payload", raw=True)))
    checks.append(("copied status logged", t.expect("address copied to clipboard", what="copy log line")))
    t.send(b"1")
    t.send(b"5")
    checks.append(("donate tab round-trip", t.expect("Donate", what="tab return")))

    # 10. product truth: the db the wizard built exists
    checks.append(("wizard db written", os.path.exists(db)))

    t.quit()

    failed = [name for name, ok in checks if not ok]
    passed = sum(1 for _, ok in checks if ok)
    print(f"TUI PTY: {passed}/{len(checks)} checks passed")
    if failed:
        for name in failed:
            print(f"  FAILED: {name}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
