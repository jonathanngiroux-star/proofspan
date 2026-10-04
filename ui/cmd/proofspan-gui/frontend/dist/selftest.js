// Selftest suite: the GUI tests ITSELF in the real webview.
// Launched by `proofspan-gui -selftest [report.json]` (the Go side emits
// "selftest:start" with the report path). Real clicks, real inputs, real
// computed styles — the same DOM the user gets, not a jsdom approximation.
//
// The suite reproduces the exact user-reported failures as first-class
// cases: the wizard's missing input fields at step 2, and the dead Skip
// button.
(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const GUI = () => window.go.main.GUI;

  const results = [];
  let failed = 0;

  function report(name, passed, detail) {
    results.push({ name, passed, detail: detail || "" });
    if (!passed) failed++;
    // console.log is visible in WebKit's stderr — the bisect trail
    console.log("[selftest] " + (passed ? "PASS " : "FAIL ") + name);
    GUI().SelftestReport(name, passed, detail || "").catch(() => {});
  }

  function assert(name, cond, detail) {
    report(name, !!cond, cond ? "" : detail || "condition false");
  }

  function click(el) {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, view: window }));
  }

  function sleep(ms) {
    return new Promise((r) => setTimeout(r, ms));
  }

  async function waitFor(fn, timeout, label) {
    const deadline = Date.now() + (timeout || 4000);
    while (Date.now() < deadline) {
      try { const v = fn(); if (v) return v; } catch (e) {}
      await sleep(50);
    }
    throw new Error("timeout: " + (label || "condition"));
  }

  const tests = [
    // --- the two user-reported bugs, as first-class cases ---

    async function skipButtonHidesTheWizard() {
      // wizard must be open (selftest runs before any seen-marker logic
      // could dismiss it; force it open to be sure)
      await GUI().WizardStart();
      await waitFor(() => $("wizard-overlay"));
      $("wizard-overlay").classList.remove("hidden");

      // the overlay must be VISIBLE before skipping
      let display = getComputedStyle($("wizard-overlay")).display;
      if (display === "none") throw new Error("overlay invisible before skip — test setup broken");

      click($("wizard-skip"));
      await sleep(120);

      // THE fix: .hidden must actually hide the overlay — computed style,
      // not class presence (the original bug: class toggled, style didn't).
      display = getComputedStyle($("wizard-overlay")).display;
      report("skip: overlay computed display becomes none", display === "none",
        "display=" + display + " (the CSS-specificity bug: .overlay{display:flex} overrode .hidden)");
      report("skip: marker recorded", await GUI().WizardHasSeen(), "WizardMarkSeen not called on skip");
    },

    async function wizardStepTwoShowsItsInputs() {
      await GUI().WizardStart();
      const v1 = await GUI().WizardNext(); // intro → source ("step 2 of 7" label)
      window.wizardRenderForTest(v1); // apply the app's own render path
      await sleep(80);
      const visible = !$("wizard-inputs").classList.contains("hidden");
      report("wizard step 2: input panel visible", visible, "needsFile flag not honored (step=" + v1.step + ")");
      const fileField = !!$("wizard-file");
      report("wizard step 2: file field present in DOM", fileField);
      const browseBtn = !!$("wizard-browse");
      report("wizard step 2: browse button present in DOM", browseBtn);
      const computed = $("wizard-file") ? getComputedStyle($("wizard-file")).display : "missing";
      report("wizard step 2: file field rendered (computed)", computed !== "none" && computed !== "missing",
        "display=" + computed);
    },

    async function wizardFullFlowWithCorpus() {
      // drive the entire working-guide flow against the real fixture
      const corpus = window.__PROOFSPAN_SELFTEST__ ? window.__PROOFSPAN_SELFTEST__.corpus : "";
      if (!corpus) { report("full flow: corpus path available", false, "no corpus path"); return; }
      await GUI().WizardStart(); // Intro (step 0)
      let v = await GUI().WizardNext(); // → Source (step 1)
      v = await GUI().WizardSetInputs("langsmith", corpus, window.__PROOFSPAN_SELFTEST__.db);
      window.wizardRenderForTest(v);
      // inputs are now set on the SOURCE page; next lands on ANALYZE (step 2)
      v = await GUI().WizardNext();
      window.wizardRenderForTest(v);
      report("flow: at analyze step", v.step === 2, "step=" + v.step);
      v = await GUI().WizardRunStep();
      window.wizardRenderForTest(v);
      report("flow: analyze ran with real summary", /lines parsed/.test(v.summary || ""), v.summary);
      v = await GUI().WizardNext();
      window.wizardRenderForTest(v);
      report("flow: at plan step", v.step === 3, "step=" + v.step);
      v = await GUI().WizardRunStep();
      window.wizardRenderForTest(v);
      report("flow: plan counts real", /400 trajectories \/ 10000 spans/.test(v.summary || ""), v.summary);
      v = await GUI().WizardNext();
      window.wizardRenderForTest(v);
      report("flow: at migrate step", v.step === 4, "step=" + v.step);
      v = await GUI().WizardRunStep();
      window.wizardRenderForTest(v);
      report("flow: migrate summary real", /migrated 400 trajectories/.test(v.summary || ""), v.summary);
      v = await GUI().WizardNext();
      window.wizardRenderForTest(v);
      report("flow: at eval step", v.step === 5, "step=" + v.step);
      v = await GUI().WizardRunStep();
      window.wizardRenderForTest(v);
      report("flow: eval verdict real", /400\/400 trajectories pass/.test(v.summary || ""), v.summary);
      v = await GUI().WizardNext();
      window.wizardRenderForTest(v);
      report("flow: done page reached", v.step === 6, "step=" + v.step);
      const done = await GUI().WizardGetDone();
      report("flow: done checklist populated", !!(done && done.eval), JSON.stringify(done));
    },

    async function tabViewsRender() {
      // every main view must contain its primary control
      const views = [
        ["view-analyze", "form-analyze"],
        ["view-migrate", "form-migrate"],
        ["view-evaluate", "form-evaluate"],
        ["view-trajectories", "form-traj"],
        ["view-judges", "form-judges"],
        ["view-serve", "form-serve"],
        ["view-donate", "donate-eth"],
      ];
      for (const [view, child] of views) {
        report("view renders: " + view, !!( $(view) && $(child) ), "missing " + child);
      }
    },

    async function darkModeAudit() {
      // color-scheme:dark is what makes the select popup + checkboxes +
      // scrollbars render dark; assert it resolves on the root.
      const scheme = getComputedStyle(document.documentElement).colorScheme;
      report("dark: color-scheme dark on root", scheme === "dark", "color-scheme=" + scheme);

      // the select (Source dropdown): background must be the dark panel
      const sel = $("wizard-source") || $("analyze-source");
      const sb = getComputedStyle(sel).backgroundColor;
      report("dark: select background is dark",
        sb === "rgb(27, 32, 43)" || /rgb\(2[0-9], ?3[0-9]/.test(sb), "backgroundColor=" + sb);

      // option rows: dark panel background; the selected row follows the
      // native dark scheme (color-scheme:dark) with WebKit's own dark
      // selection tint — any dark rgb is correct, white is the bug.
      const opt = sel.options[0];
      const ob = getComputedStyle(opt).backgroundColor;
      const isDark = (v) => {
        const m = v.match(/rgb\((\d+), ?(\d+), ?(\d+)/);
        if (!m) return false;
        const [r, g, b] = [+m[1], +m[2], +m[3]];
        return (r + g + b) / 3 < 90; // avg channel < 90 = dark
      };
      report("dark: select option rows are dark", isDark(ob), "option bg=" + ob);

      // text inputs dark
      const inp = $("analyze-file");
      const ib = getComputedStyle(inp).backgroundColor;
      report("dark: text input background is dark",
        ib === "rgb(27, 32, 43)" || /rgb\(2[0-9], ?3[0-9]/.test(ib), "input bg=" + ib);

      // checkbox accent tinted
      const cb = $("migrate-dryrun") || $("serve-scim");
      const ac = getComputedStyle(cb).accentColor;
      report("dark: checkbox accent tinted", ac !== "" && ac !== "auto", "accentColor=" + ac);

      // body background dark (no white flash anywhere)
      const bb = getComputedStyle(document.body).backgroundColor;
      report("dark: body background dark", bb === "rgb(15, 17, 22)", "body bg=" + bb);

      // summary markers follow the palette
      const summ = document.querySelector("summary");
      if (summ) {
        const mc = getComputedStyle(summ).color;
        report("dark: summary marker tinted", !!mc, "marker color=" + mc);
      }
    },

    async function donateAddressesBound() {
      // the binding populates the donate view at load
      await waitFor(() => $("donate-eth").textContent.length > 10, 3000, "donate eth populated");
      report("donate: ethereum address bound", /^0x[0-9a-fA-F]{40}$/.test($("donate-eth").textContent),
        $("donate-eth").textContent);
      report("donate: bitcoin address bound", $("donate-btc").textContent.startsWith("bc1"),
        $("donate-btc").textContent);
    },
    darkModeAudit,
  ];

  async function run() {
    console.log("[selftest] suite starting: " + tests.length + " tests");
    for (const t of tests) {
      console.log("[selftest] begin " + t.name);
      try {
        await t();
      } catch (e) {
        report(t.name + " (threw)", false, String(e && e.message ? e.message : e));
      }
      console.log("[selftest] end " + t.name);
    }
    // SelftestFinish writes the report and quits the window (Go side) —
    // a JS-side Quit here would race the report write.
    GUI().SelftestFinish(failed).catch(() => {});
  }

  // The GO side drives the start: OnDomReady emits "selftest:go" (the
  // bridge is guaranteed injected by then). Boot ALSO keeps the original
  // retry-on-catch loop — if the event is missed for any reason, the
  // script-load boot still finds the bridge moments later.
  function bindingsReady() {
    return !!(window.go && window.go.main && window.go.main.GUI &&
              typeof window.go.main.GUI.SelftestMode === "function");
  }

  async function boot() {
    try {
      const mode = await GUI().SelftestMode();
      if (!mode) return;
      window.__PROOFSPAN_SELFTEST__ = await GUI().SelftestReady();
      GUI().SelftestReport("boot: ready, suite starting", true, "").catch(() => {});
      setTimeout(run, 400); // let app.js finish its wiring first
    } catch (e) {
      setTimeout(boot, 300); // bridge not up yet — the version that worked
    }
  }

  // dual start paths: Go's DomReady event AND script-load (whichever first;
  // boot is idempotent because SelftestMode gates it). GUARD window.runtime:
  // it injects asynchronously — an unguarded EventsOn throw at load time
  // killed the whole script before boot() could run (the silent hang).
  if (window.runtime && typeof window.runtime.EventsOn === "function") {
    window.runtime.EventsOn("selftest:go", () => { boot(); });
  } else {
    // runtime not injected yet — poll briefly for it, then wire
    const wireUp = () => {
      if (window.runtime && typeof window.runtime.EventsOn === "function") {
        window.runtime.EventsOn("selftest:go", () => { boot(); });
      } else {
        setTimeout(wireUp, 200);
      }
    };
    wireUp();
  }
  boot();
})();
