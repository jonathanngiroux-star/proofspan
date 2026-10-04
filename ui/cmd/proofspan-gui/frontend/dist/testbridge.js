// In-app test-bridge interpreter (the FogOS pattern, generalized).
//
// The Go side (testbridge.go) forwards each JSON command from the command
// file as a "test:cmd" event — ONLY when the app was launched with
// PROOFSPAN_GUI_TESTBRIDGE=1. This file listens for that event and runs
// the command against the REAL DOM: clicks fire real handlers, fill hits
// real inputs (with input/change events so the app's listeners react),
// read returns live state. Results go back through the TestResult
// binding → `TEST-RESULT {json}` in the result log.
//
// The interpreter is a fixed switch — no eval — so the page CSP holds and
// the attack surface stays "whatever the switch does", nothing more.
(function () {
  "use strict";

  if (!window.runtime || typeof window.runtime.EventsOn !== "function") return;

  var armed = false;

  function result(cmd, ok, value, error) {
    var payload;
    try {
      payload = JSON.stringify({
        id: cmd && cmd.id ? cmd.id : "",
        ok: !!ok,
        value: value === undefined ? null : value,
        error: error || null,
      });
    } catch (e) {
      payload = JSON.stringify({ id: "", ok: false, value: null, error: "serialize: " + e });
    }
    if (window.go && window.go.main && window.go.main.GUI && window.go.main.GUI.TestResult) {
      window.go.main.GUI.TestResult(payload).catch(function () {});
    }
  }

  function fire(el, type) {
    el.dispatchEvent(new Event(type, { bubbles: true }));
  }

  function clickEl(el) {
    el.scrollIntoView({ block: "center" });
    el.click();
  }

  function selector(sel) {
    return document.querySelector(sel);
  }

  function fillEl(el, text) {
    el.focus();
    el.value = "";
    // type in chunks so input listeners see progressive edits (like a user)
    document.execCommand && document.execCommand("insertText", false, text);
    if (el.value !== text) el.value = text; // execCommand refused (shadow DOM, etc.)
    fire(el, "input");
    fire(el, "change");
  }

  function interpret(cmd) {
    switch (cmd.op) {
      case "click": {
        var el = selector(cmd.sel);
        if (!el) return result(cmd, false, null, "no element for " + cmd.sel);
        clickEl(el);
        return result(cmd, true, null, null);
      }
      case "clickText": {
        // find a clickable element whose text contains cmd.text, scoped to
        // sel (default: the whole document)
        var root = cmd.sel ? selector(cmd.sel) : document;
        if (!root) return result(cmd, false, null, "no scope for " + cmd.sel);
        var want = (cmd.text || "").trim().toLowerCase();
        var candidates = root.querySelectorAll("button, a, [role=button], input[type=submit], input[type=button], li, summary");
        var hit = null;
        for (var i = 0; i < candidates.length; i++) {
          var t = (candidates[i].textContent || candidates[i].value || "").trim().toLowerCase();
          if (want && t.indexOf(want) !== -1) { hit = candidates[i]; break; }
        }
        if (!hit) return result(cmd, false, null, "no clickable with text " + cmd.text);
        clickEl(hit);
        return result(cmd, true, null, null);
      }
      case "fill": {
        var f = selector(cmd.sel);
        if (!f) return result(cmd, false, null, "no element for " + cmd.sel);
        fillEl(f, String(cmd.text !== undefined ? cmd.text : ""));
        return result(cmd, true, f.value, null);
      }
      case "select": {
        var s = selector(cmd.sel);
        if (!s) return result(cmd, false, null, "no element for " + cmd.sel);
        var opt = null;
        for (var j = 0; j < s.options.length; j++) {
          if (s.options[j].value === cmd.value || s.options[j].textContent === cmd.value) { opt = s.options[j]; break; }
        }
        if (!opt) return result(cmd, false, null, "no option " + cmd.value);
        s.value = opt.value;
        fire(s, "input");
        fire(s, "change");
        return result(cmd, true, s.value, null);
      }
      case "check": {
        var c = selector(cmd.sel);
        if (!c) return result(cmd, false, null, "no element for " + cmd.sel);
        if (cmd.checked !== undefined) {
          if (c.checked !== !!cmd.checked) { clickEl(c); }
        } else if (!c.checked) {
          clickEl(c);
        }
        return result(cmd, true, c.checked, null);
      }
      case "key": {
        // real key event at the focused element (Enter in a form submits it)
        var target = document.activeElement || document.body;
        var ev = new KeyboardEvent(cmd.event || "keydown", {
          key: cmd.key || "", code: cmd.code || "", keyCode: cmd.keycode || 0,
          which: cmd.keycode || 0, bubbles: true, cancelable: true,
        });
        target.dispatchEvent(ev);
        if (cmd.event === "keydown" && (cmd.key === "Enter")) {
          // forms: the submit path (a real Enter in a text input submits)
          var form = target.closest ? target.closest("form") : null;
          if (form && target.tagName === "INPUT") {
            var submit = form.querySelector('button[type="submit"], button:not([type])');
            if (submit) clickEl(submit);
          }
        }
        return result(cmd, true, null, null);
      }
      case "read": {
        var r = selector(cmd.sel);
        if (!r) return result(cmd, false, null, "no element for " + cmd.sel);
        var cs;
        try { cs = getComputedStyle(r); } catch (e) { cs = null; }
        var out = {
          text: r.textContent,
          value: r.value !== undefined ? r.value : null,
          checked: r.checked !== undefined ? !!r.checked : null,
          display: cs ? cs.display : null,
          background: cs ? cs.backgroundColor : null,
          color: cs ? cs.color : null,
          colorScheme: (function () { try { return getComputedStyle(document.documentElement).colorScheme; } catch (e) { return null; } })(),
          cls: r.className || "",
          disabled: !!r.disabled,
        };
        if (cmd.attr) out.attr = r.getAttribute(cmd.attr);
        return result(cmd, true, out, null);
      }
      case "count": {
        return result(cmd, true, document.querySelectorAll(cmd.sel).length, null);
      }
      case "texts": {
        var nodes = document.querySelectorAll(cmd.sel);
        var arr = [];
        for (var k = 0; k < nodes.length; k++) arr.push(nodes[k].textContent.trim());
        return result(cmd, true, arr, null);
      }
      case "log": {
        var lg = selector("#log");
        return result(cmd, true, lg ? lg.textContent : null, null);
      }
      case "status": {
        var st = selector("#status");
        return result(cmd, true, st ? st.textContent : null, null);
      }
      case "ping":
        return result(cmd, true, "pong", null);
      default:
        return result(cmd, false, null, "unknown op: " + cmd.op);
    }
  }

  window.runtime.EventsOn("test:cmd", function (cmd) {
    armed = true;
    try {
      interpret(cmd || {});
    } catch (e) {
      result(cmd, false, null, "interpreter: " + e);
    }
  });
})();
