// Proofspan GUI — thin driver over the Wails-bound Go methods. No product
// logic lives here; every action calls a binding and renders the result.
(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const out = $("log");
  const statusEl = $("status");

  function log(line) {
    out.textContent += line + "\n";
    out.scrollTop = out.scrollHeight;
  }
  function setStatus(text) { statusEl.textContent = text; }
  function err(e) {
    // Wails rejects promises with [Error: message] — show the real text.
    const msg = String(e && e.message ? e.message : e);
    log("error: " + msg);
    setStatus("Error — see output");
    return msg;
  }

  // ---- tabs ----
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      document.querySelectorAll(".view").forEach((v) => v.classList.remove("active"));
      tab.classList.add("active");
      $("view-" + tab.dataset.view).classList.add("active");
    });
  });

  // ---- shared events from Go ----
  if (window.go && window.go.main && window.go.main.GUI) {
    window.runtime.EventsOn("output", (data) => { if (data && data.line) log(data.line); });
    window.runtime.EventsOn("status", (data) => { if (data && data.text) setStatus(data.text); });
    window.runtime.EventsOn("serve:state", (data) => { if (data) setStatus("Serve: " + (data.running ? "running at " + data.addr : "stopped")); });
  }

  const GUI = () => window.go.main.GUI;

  // ---- donate (load once, exact addresses from the binding) ----
  GUI().GetDonateAddresses().then((d) => {
    $("donate-eth").textContent = d.ethereum;
    $("donate-btc").textContent = d.bitcoin;
    // the wizard's done step shows the same addresses (audit: byte-for-byte
    // on the donate view AND the last wizard step)
    if ($("done-eth")) $("done-eth").textContent = d.ethereum;
    if ($("done-btc")) $("done-btc").textContent = d.bitcoin;
  }).catch((e) => err(e));
  document.querySelectorAll(".copy").forEach((btn) => {
    btn.addEventListener("click", () => {
      const text = $(btn.dataset.copy).textContent;
      // The packaged webview runs on a wails:// origin — NOT a secure
      // context, so navigator.clipboard is undefined there. Fall back to
      // the native Wails bridge (Go: CopyToClipboard → runtime.ClipboardSetText).
      const write = (navigator.clipboard && navigator.clipboard.writeText)
        ? navigator.clipboard.writeText(text)
        : Promise.reject(new Error("no web clipboard"));
      write.then(() => {
        btn.textContent = "Copied";
        setTimeout(() => (btn.textContent = "Copy"), 1200);
      }).catch(() => {
        if (window.go && window.go.main && window.go.main.GUI && window.go.main.GUI.CopyToClipboard) {
          window.go.main.GUI.CopyToClipboard(text).then(() => {
            btn.textContent = "Copied";
            setTimeout(() => (btn.textContent = "Copy"), 1200);
          }).catch((e) => err(e));
        }
      });
    });
  });

  // ---- first-run wizard (a WORKING guide: inputs, run, summaries, output) ----
  const WIZARD_SEEN_KEY = "proofspan.wizard.seen";
  const overlay = $("wizard-overlay");

  // exposed at load for the selftest suite (which drives bindings directly
  // and must apply the app's own render path to see the DOM effect)
  window.wizardRenderForTest = (v) => wizardRender(v);

  function wizardRender(v, error) {
    $("wizard-title").textContent = v.title;
    $("wizard-progress").textContent = "step " + (v.step + 1) + " of 7";
    $("wizard-body").textContent = v.body;

    // inputs: visible on the source step (needsFile from the backend,
    // camelCase — the original bug was reading it as needs_file)
    const inputs = $("wizard-inputs");
    if (v.needsFile) {
      inputs.classList.remove("hidden");
      $("wizard-source").value = v.source;
      if (document.activeElement !== $("wizard-file")) $("wizard-file").value = v.file;
      if (document.activeElement !== $("wizard-db")) $("wizard-db").value = v.db;
    } else {
      inputs.classList.add("hidden");
    }

    // error line
    const errEl = $("wizard-error");
    if (error) {
      errEl.textContent = error;
      errEl.classList.remove("hidden");
    } else {
      errEl.classList.add("hidden");
    }

    // this step's one-line summary
    const sum = $("wizard-summary");
    if (v.summary) {
      sum.textContent = v.summary;
      sum.classList.remove("hidden");
    } else {
      sum.classList.add("hidden");
    }

    // raw output tail (the main log is behind the overlay)
    const tail = $("wizard-tail");
    if (v.tail && v.tail.length) {
      tail.textContent = v.tail.join("\n");
      tail.classList.remove("hidden");
    } else {
      tail.classList.add("hidden");
    }

    // done checklist
    const doneEl = $("wizard-done");
    if (v.step === 6) {
      const d = v.done || {};
      $("done-analyzed").textContent = d.analyzed || "—";
      $("done-planned").textContent = d.planned || "—";
      $("done-migrated").textContent = d.migrated || "—";
      $("done-eval").textContent = d.eval || "—";
      doneEl.classList.remove("hidden");
    } else {
      doneEl.classList.add("hidden");
    }

    // step chrome
    $("wizard-back").disabled = v.step === 0;
    $("wizard-run").classList.toggle("hidden", !v.runnable);
    $("wizard-run").disabled = !!v.running || !v.runnable;
    if (v.runnable && !v.ranStep) {
      $("wizard-next").disabled = true; // run the step first — the guide walks
    } else {
      $("wizard-next").disabled = false;
    }
    $("wizard-next").textContent = v.step === 6 ? "Close" : "Next";
    $("wizard-skip").textContent = v.step === 6 ? "Close" : "Skip — I know my way";
  }

  function wizardError(e) {
    const msg = String(e && e.message ? e.message : e);
    wizardRender(wizardLast, msg);
    return msg;
  }

  let wizardLast = null;

  function wizardPersistInputs() {
    return GUI().WizardSetInputs(
      $("wizard-source").value,
      $("wizard-file").value,
      $("wizard-db").value
    );
  }

  function wizardOpen() {
    GUI().WizardStart().then((v) => {
      wizardLast = v;
      wizardRender(v);
      overlay.classList.remove("hidden");
      $("wizard-next").focus();
    }).catch((e) => err(e));
  }

  $("wizard-next").addEventListener("click", () => {
    if (wizardLast && wizardLast.needsFile) {
      // persist the typed inputs, then advance (backend re-validates)
      wizardPersistInputs()
        .then(() => GUI().WizardNext())
        .then((v) => { wizardLast = v; wizardRender(v); })
        .catch(wizardError);
      return;
    }
    if (wizardLast && wizardLast.step === 6) {
      overlay.classList.add("hidden"); // hide FIRST — nothing may block dismissal
      GUI().WizardMarkSeen().catch(() => {});
      try { localStorage.setItem(WIZARD_SEEN_KEY, "1"); } catch (e) { /* webview storage may be blocked */ }
      return;
    }
    GUI().WizardNext().then((v) => { wizardLast = v; wizardRender(v); }).catch(wizardError);
  });

  $("wizard-back").addEventListener("click", () => {
    GUI().WizardBack().then((v) => { wizardLast = v; wizardRender(v); }).catch(wizardError);
  });

  $("wizard-run").addEventListener("click", () => {
    $("wizard-run").disabled = true;
    setStatus("Wizard: running step…");
    GUI().WizardRunStep().then((v) => {
      wizardLast = v;
      wizardRender(v);
      setStatus("Wizard: " + (v.summary || "step done"));
    }).catch(wizardError).finally(() => { $("wizard-run").disabled = false; });
  });

  $("wizard-browse").addEventListener("click", () => {
    GUI().WizardBrowseFile().then((path) => {
      if (path) {
        $("wizard-file").value = path;
        wizardPersistInputs().then((v) => { wizardLast = v; wizardRender(v); }).catch(wizardError);
      }
    }).catch(wizardError);
  });

  $("wizard-skip").addEventListener("click", () => {
    overlay.classList.add("hidden"); // hide FIRST — the button must always work
    GUI().WizardMarkSeen().catch(() => {});
    try { localStorage.setItem(WIZARD_SEEN_KEY, "1"); } catch (e) { /* webview storage may be blocked */ }
  });

  // re-openable from the tab bar (the wizard is a guide, not a gate)
  const wizTab = document.createElement("button");
  wizTab.className = "tab";
  wizTab.dataset.view = "wizard";
  wizTab.accessKey = "0";
  wizTab.textContent = "Wizard";
  wizTab.addEventListener("click", wizardOpen);
  $("tabs").appendChild(wizTab);

  // Esc closes the overlay (the tabs stay fully usable without it)
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && !overlay.classList.contains("hidden")) {
      overlay.classList.add("hidden");
      if (wizardLast && wizardLast.step === 6) {
        GUI().WizardMarkSeen().catch(() => {});
      }
    }
  });

  // first run: open automatically once. The backend marker file is
  // authoritative (webview storage can be blocked); localStorage only
  // short-circuits when it is readable AND says seen.
  let seenHint = false;
  try { seenHint = !!localStorage.getItem(WIZARD_SEEN_KEY); } catch (e) {}
  if (!seenHint) {
    GUI().WizardHasSeen().then((seen) => { if (!seen) wizardOpen(); }).catch(() => wizardOpen());
  }

  // ---- analyze ----
  $("form-analyze").addEventListener("submit", (ev) => {
    ev.preventDefault();
    GUI().RunReport({
      source: $("analyze-source").value,
      file: $("analyze-file").value,
      outDir: $("analyze-out").value,
    }).catch((e) => err(e));
  });

  // ---- migrate ----
  $("form-migrate").addEventListener("submit", (ev) => {
    ev.preventDefault();
    GUI().RunMigrate({
      source: $("migrate-source").value,
      file: $("migrate-file").value,
      db: $("migrate-db").value,
      dryRun: $("migrate-dryrun").checked,
    }).catch((e) => err(e));
  });
  $("migrate-plan").addEventListener("click", () => {
    const pane = $("plan-result");
    const btn = $("migrate-plan");
    btn.disabled = true;
    GUI().PlanMigration({
      source: $("migrate-source").value,
      file: $("migrate-file").value,
      db: $("migrate-db").value,
      dryRun: true,
    }).then((plan) => {
      // NOTE: the Go side emits snake_case JSON tags (DryRunPlan in bindings.go)
      $("plan-meta").textContent = plan.source + " → " + plan.target_version + ", " + plan.input_file;
      $("plan-summary").innerHTML = [
        stat(plan.trajectories, "trajectories"),
        stat(plan.runs_read, "runs read"),
        stat(plan.spans_planned, "spans planned"),
        stat(plan.field_mappings ? plan.field_mappings.length : 0, "fields mapped"),
        stat(plan.dropped_fields ? plan.dropped_fields.length : 0, "dropped fields"),
      ].join("");
      $("plan-map-count").textContent = plan.field_mappings ? plan.field_mappings.length : 0;
      $("plan-dropped") != null && ($("plan-drop-count").textContent = plan.dropped_fields ? plan.dropped_fields.length : 0);
      rows($("plan-mappings"), plan.field_mappings, (m) => [m.from, m.to, m.note || ""]);
      rows($("plan-dropped"), plan.dropped_fields, (d) => [d.field, d.note]);
      $("plan-sample").textContent = plan.sample_span
        ? JSON.stringify(plan.sample_span, null, 2)
        : "(no sample span in plan)";
      pane.classList.remove("hidden");
      setStatus("Dry-run plan rendered — " + plan.spans_planned + " spans planned, " +
        (plan.dropped_fields ? plan.dropped_fields.length : 0) + " dropped fields");
    }).catch((e) => err(e)).finally(() => { btn.disabled = false; });
  });

  function stat(v, label) {
    return `<div class="stat"><b>${v}</b>${label}</div>`;
  }
  function rows(tbody, items, cells) {
    tbody.innerHTML = "";
    (items || []).forEach((item) => {
      const tr = document.createElement("tr");
      cells(item).forEach((c) => {
        const td = document.createElement("td");
        td.textContent = c;
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    if (!items || items.length === 0) {
      const tr = document.createElement("tr");
      const td = document.createElement("td");
      td.colSpan = 3;
      td.textContent = "(none)";
      tr.appendChild(td);
      tbody.appendChild(tr);
    }
  }

  // ---- evaluate ----
  function evalConfig() {
    return {
      db: $("eval-db").value,
      trajectory: $("eval-trajectory").value,
      assertions: $("eval-assertions").value,
      judgesRun: $("eval-judges").value,
      judgeEndpoint: $("eval-endpoint").value,
      judgeAPIKey: $("eval-apikey").value,
    };
  }
  $("form-evaluate").addEventListener("submit", (ev) => {
    ev.preventDefault();
    GUI().RunEval(evalConfig()).catch((e) => err(e));
  });
  $("eval-reports").addEventListener("click", () => {
    const btn = $("eval-reports");
    btn.disabled = true;
    GUI().RunEvalGetReports(evalConfig()).then((reports) => {
      const pane = $("eval-result");
      const tbody = $("eval-rows");
      tbody.innerHTML = "";
      let pass = 0, fail = 0;
      (reports || []).forEach((r) => {
        const ok = r.failed === 0 && r.errored === 0 && r.total > 0;
        ok ? pass++ : fail++;
        const tr = document.createElement("tr");
        tr.className = ok ? "pass" : "fail";
        // NOTE: TrajectoryReportView emits snake_case (bindings.go) — trajectory_id
        [r.trajectory_id, ok ? "pass" : (r.errored > 0 ? "error" : "fail"), r.passed, r.failed, r.errored]
          .forEach((c) => {
            const td = document.createElement("td");
            td.textContent = c;
            tr.appendChild(td);
          });
        tbody.appendChild(tr);
      });
      if ((reports || []).length === 0) {
        tbody.innerHTML = '<tr><td colspan="5">(no reports — check the DB path)</td></tr>';
      }
      $("eval-meta").textContent = pass + " pass / " + fail + " fail";
      pane.classList.remove("hidden");
      setStatus("Eval: " + pass + " pass / " + fail + " fail");
      log("[report table: " + pass + " pass / " + fail + " fail]");
    }).catch((e) => err(e)).finally(() => { btn.disabled = false; });
  });

  // ---- trajectories ----
  $("form-traj").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const list = $("traj-list");
    list.classList.remove("hidden");
    list.innerHTML = '<li class="empty">Loading…</li>';
    GUI().ListTrajectories($("traj-db").value).then((ids) => {
      list.innerHTML = "";
      if (!ids || ids.length === 0) {
        list.innerHTML = '<li class="empty">No trajectories — migrate an export first.</li>';
        return;
      }
      ids.forEach((id) => {
        const li = document.createElement("li");
        li.textContent = id;
        li.tabIndex = 0;
        li.addEventListener("click", () => loadTrajectory(id, li));
        li.addEventListener("keydown", (ev2) => {
          if (ev2.key === "Enter") loadTrajectory(id, li);
        });
        list.appendChild(li);
      });
      setStatus("Loaded " + ids.length + " trajectories");
    }).catch((e) => {
      list.innerHTML = '<li class="empty">Failed — see output</li>';
      err(e);
    });
  });

  function loadTrajectory(id, li) {
    document.querySelectorAll("#traj-list li").forEach((n) => n.classList.remove("selected"));
    li.classList.add("selected");
    $("traj-detail").classList.remove("hidden");
    $("traj-title").textContent = id;
    $("span-rows").innerHTML = "";
    GUI().GetTrajectory($("traj-db").value, id).then((bundle) => {
      const tb = $("span-rows");
      (bundle.spans || []).forEach((s) => {
        const dur = s.endedAt - s.startedAt;
        const tr = document.createElement("tr");
        tr.tabIndex = 0;
        const cells = [s.name, s.kind, dur >= 0 ? dur + "ms" : "?"];
        cells.forEach((c) => {
          const td = document.createElement("td");
          td.textContent = c;
          tr.appendChild(td);
        });
        tr.addEventListener("click", () => showSpanDetail(s));
        tr.addEventListener("keydown", (ev2) => { if (ev2.key === "Enter") showSpanDetail(s); });
        tb.appendChild(tr);
      });
      if ((bundle.spans || []).length === 0) {
        tb.innerHTML = '<tr><td colspan="3">(no spans)</td></tr>';
      }
    }).catch((e) => err(e));
  }

  function showSpanDetail(s) {
    const pre = $("span-detail");
    pre.classList.remove("hidden");
    pre.textContent = JSON.stringify(s, null, 2);
  }

  // ---- judges ----
  $("form-judges").addEventListener("submit", (ev) => {
    ev.preventDefault();
    GUI().GetJudgeManifest($("judges-db").value).then((m) => {
      $("judges-ns").textContent = m.namespace + " — " + m.version;
      rows($("judges-providers"), Object.entries(m.providers || {}), ([name, p]) => [name, p.endpoint, p.api_key_env || ""]);
      rows($("judges-rows"), m.judges, (j) => [j.id, j.model_fingerprint, j.provider || "", j.description]);
      $("judges-result").classList.remove("hidden");
      setStatus("Manifest: " + (m.judges ? m.judges.length : 0) + " judges pinned");
    }).catch((e) => err(e));
  });

  // ---- serve ----
  $("serve-start").addEventListener("click", () => {
    GUI().StartServe({
      db: $("serve-db").value,
      addr: $("serve-addr").value,
      scim: $("serve-scim").checked,
      scimToken: $("serve-token").value,
    }).catch((e) => err(e));
  });
  $("serve-stop").addEventListener("click", () => {
    GUI().StopServe().catch((e) => err(e));
  });

  // ---- log ----
  $("log-clear").addEventListener("click", () => { out.textContent = ""; });

  // ---- keyboard shortcuts (audit: keyboard path for primary actions) ----
  document.addEventListener("keydown", (ev) => {
    if (ev.altKey) {
      const n = parseInt(ev.key, 10);
      if (n >= 1 && n <= 7) {
        document.querySelectorAll(".tab")[n - 1].click();
        ev.preventDefault();
      }
    }
    // Alt+Enter runs the primary action of the visible view
    if (ev.altKey && ev.key === "Enter") {
      const view = document.querySelector(".view.active");
      const btn = view && view.querySelector("button.primary");
      if (btn) { btn.click(); ev.preventDefault(); }
    }
  });
})();
