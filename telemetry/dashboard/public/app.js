// CodeCanary Analytics dashboard — fetches aggregates from /api/* and
// renders them with Chart.js. State (range + filters) is kept in the URL
// so dashboards are bookmarkable.

const FILTER_KEYS = [
  "platform",
  "provider",
  "review_model",
  "triage_model",
  "installation",
];

const state = {
  range: "30d",
  filters: {
    platform: "",
    provider: "",
    review_model: "",
    triage_model: "",
    installation: "",
  },
  charts: {},
};

// Palette tuned to work in both light and dark modes.
const PALETTE = [
  "#e8b900",
  "#3b82f6",
  "#10b981",
  "#ef4444",
  "#8b5cf6",
  "#f97316",
  "#14b8a6",
  "#ec4899",
  "#64748b",
];

const SEVERITY_COLORS = {
  critical: "#dc2626",
  bug: "#ef4444",
  warning: "#f59e0b",
  suggestion: "#3b82f6",
  nitpick: "#94a3b8",
};

const currencyFmt = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 2,
});

const numberFmt = new Intl.NumberFormat("en-US");

function formatDuration(ms) {
  if (!ms || ms <= 0) return "—";
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const mins = Math.floor(seconds / 60);
  const rem = Math.round(seconds % 60);
  return `${mins}m ${rem}s`;
}

function formatDay(isoish) {
  const s = String(isoish).replace(" ", "T");
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return String(isoish);
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// Short form of an installation UUID for dropdown display. 8 chars
// mirrors the git short-hash convention.
function shortHash(uuid) {
  return uuid.slice(0, 8);
}

// Swap the current options in a <select> for a grouped layout.
// groups: [{ label, options: [{ value, text }] }]
function renderSelectOptions(select, groups, currentValue) {
  // Preserve the "All" option.
  while (select.options.length > 1) select.remove(1);

  let known = false;
  for (const group of groups) {
    if (!group.options.length) continue;
    const og = document.createElement("optgroup");
    og.label = group.label;
    for (const opt of group.options) {
      const el = document.createElement("option");
      el.value = opt.value;
      el.textContent = opt.text;
      og.appendChild(el);
      if (opt.value === currentValue) known = true;
    }
    select.appendChild(og);
  }

  // Preserve a bookmarked value even if it's not in the freshly
  // fetched filter list (e.g. a model that only appeared in a
  // different range).
  if (currentValue && !known) {
    const el = document.createElement("option");
    el.value = currentValue;
    el.textContent = currentValue;
    select.appendChild(el);
  }
  select.value = currentValue || "";
}

// ---------------------------------------------------------------------
// URL state
// ---------------------------------------------------------------------

function loadStateFromURL() {
  const url = new URL(window.location.href);
  const range = url.searchParams.get("range");
  if (range === "7d" || range === "30d" || range === "90d") {
    state.range = range;
  }
  for (const key of FILTER_KEYS) {
    state.filters[key] = url.searchParams.get(key) || "";
  }
}

function pushStateToURL() {
  const url = new URL(window.location.href);
  url.searchParams.set("range", state.range);
  for (const key of FILTER_KEYS) {
    if (state.filters[key]) {
      url.searchParams.set(key, state.filters[key]);
    } else {
      url.searchParams.delete(key);
    }
  }
  history.replaceState(null, "", url.toString());
}

function buildQuery(extra = {}) {
  const params = new URLSearchParams();
  params.set("range", state.range);
  for (const key of FILTER_KEYS) {
    if (state.filters[key]) params.set(key, state.filters[key]);
  }
  for (const [k, v] of Object.entries(extra)) params.set(k, v);
  return params.toString();
}

function anyFilterActive() {
  return FILTER_KEYS.some((k) => !!state.filters[k]);
}

// ---------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------

async function fetchJSON(path) {
  const res = await fetch(path, { credentials: "same-origin" });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`${path} → ${res.status}: ${body || res.statusText}`);
  }
  return res.json();
}

function showError(err) {
  const node = document.getElementById("error");
  node.textContent = String(err?.message || err);
  node.hidden = false;
  console.error(err);
}

function clearError() {
  document.getElementById("error").hidden = true;
}

function setCard(key, value, sub = null) {
  const card = document.querySelector(`[data-card="${key}"]`);
  if (!card) return;
  const valueEl = card.querySelector(".card-value");
  if (valueEl) valueEl.textContent = value;
  const subEl = card.querySelector("[data-sub]");
  if (subEl) subEl.innerHTML = sub ?? "&nbsp;";
}

// ---------------------------------------------------------------------
// Chart helpers
// ---------------------------------------------------------------------

function destroyChart(key) {
  if (state.charts[key]) {
    state.charts[key].destroy();
    delete state.charts[key];
  }
}

function chartColors() {
  const style = getComputedStyle(document.documentElement);
  return {
    text: style.getPropertyValue("--text").trim() || "#1a1a1a",
    textDim: style.getPropertyValue("--text-dim").trim() || "#6b6b6b",
    border: style.getPropertyValue("--border").trim() || "#e6e4dc",
  };
}

function lineChart(canvasId, points, label, color, opts = {}) {
  destroyChart(canvasId);
  const ctx = document.getElementById(canvasId).getContext("2d");
  const colors = chartColors();
  const { asCurrency, asDuration } = opts;

  const labels = points.map((p) => formatDay(p.date));
  const data = points.map((p) => (asDuration ? p.value / 1000 : p.value));

  state.charts[canvasId] = new Chart(ctx, {
    type: "line",
    data: {
      labels,
      datasets: [
        {
          label,
          data,
          borderColor: color,
          backgroundColor: color + "22",
          borderWidth: 2,
          pointRadius: 0,
          pointHoverRadius: 4,
          tension: 0.25,
          fill: true,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: (item) => {
              const v = item.parsed.y;
              if (asCurrency) return ` ${currencyFmt.format(v)}`;
              if (asDuration) return ` ${v.toFixed(1)}s`;
              return ` ${numberFmt.format(v)}`;
            },
          },
        },
      },
      scales: {
        x: {
          grid: { display: false },
          ticks: { color: colors.textDim, maxRotation: 0, autoSkip: true },
        },
        y: {
          beginAtZero: true,
          grid: { color: colors.border },
          ticks: {
            color: colors.textDim,
            callback: (v) => {
              if (asCurrency) return currencyFmt.format(v);
              if (asDuration) return `${v}s`;
              return numberFmt.format(v);
            },
          },
        },
      },
    },
  });
}

function donutChart(canvasId, buckets, colorFor) {
  destroyChart(canvasId);
  const ctx = document.getElementById(canvasId).getContext("2d");

  if (!buckets.length) {
    ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);
    const colors = chartColors();
    ctx.fillStyle = colors.textDim;
    ctx.font = "13px system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText("No data", ctx.canvas.width / 2, ctx.canvas.height / 2);
    return;
  }

  const labels = buckets.map((b) => b.key);
  const data = buckets.map((b) => b.value);
  const colors = labels.map((key, i) =>
    typeof colorFor === "function"
      ? colorFor(key, i)
      : PALETTE[i % PALETTE.length],
  );

  state.charts[canvasId] = new Chart(ctx, {
    type: "doughnut",
    data: {
      labels,
      datasets: [
        {
          data,
          backgroundColor: colors,
          borderColor: "transparent",
          borderWidth: 0,
          hoverOffset: 6,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: "62%",
      plugins: {
        legend: {
          position: "right",
          labels: {
            color: chartColors().text,
            boxWidth: 10,
            boxHeight: 10,
            padding: 10,
          },
        },
        tooltip: {
          callbacks: {
            label: (item) => {
              const total = data.reduce((a, b) => a + b, 0);
              const pct = total > 0 ? ((item.parsed / total) * 100).toFixed(1) : 0;
              return ` ${item.label}: ${numberFmt.format(item.parsed)} (${pct}%)`;
            },
          },
        },
      },
    },
  });
}

// ---------------------------------------------------------------------
// Data loading
// ---------------------------------------------------------------------

async function loadOverview() {
  const data = await fetchJSON(`/api/overview?${buildQuery()}`);
  setCard("reviews", numberFmt.format(Math.round(data.reviews)));

  const installsTotal = Math.round(data.installations?.total ?? 0);
  const byPlatform = data.installations?.by_platform ?? {};
  const gh = Math.round(byPlatform.github ?? 0);
  const lo = Math.round(byPlatform.local ?? 0);
  // Hide the split line when a platform filter is active (one side is 0)
  // or when there's nothing to split.
  const showSplit = !state.filters.platform && installsTotal > 0;
  const sub = showSplit
    ? `<strong>${numberFmt.format(gh)}</strong> GHA · <strong>${numberFmt.format(lo)}</strong> local`
    : null;
  setCard("installations", numberFmt.format(installsTotal), sub);

  setCard("cost", currencyFmt.format(data.cost_usd));
  setCard("findings", numberFmt.format(Math.round(data.findings)));
  setCard("avg_cost", currencyFmt.format(data.avg_cost_usd || 0));
  setCard("avg_duration", formatDuration(data.avg_duration_ms));
}

async function loadTimeseries() {
  const [reviews, cost, duration] = await Promise.all([
    fetchJSON(`/api/timeseries?${buildQuery({ metric: "reviews" })}`),
    fetchJSON(`/api/timeseries?${buildQuery({ metric: "cost" })}`),
    fetchJSON(`/api/timeseries?${buildQuery({ metric: "duration" })}`),
  ]);

  lineChart("chart-reviews", reviews.points, "Reviews", PALETTE[0]);
  lineChart("chart-cost", cost.points, "Cost", PALETTE[1], { asCurrency: true });
  lineChart("chart-duration", duration.points, "Avg duration", PALETTE[2], {
    asDuration: true,
  });
}

async function loadBreakdowns() {
  const dims = [
    "provider",
    "platform",
    "review_model",
    "triage_model",
    "severity",
    "os",
    "incremental",
    "version",
  ];
  const results = await Promise.all(
    dims.map((dim) => fetchJSON(`/api/breakdown?${buildQuery({ dim })}`)),
  );

  const by = Object.fromEntries(dims.map((d, i) => [d, results[i]]));

  donutChart("chart-provider", by.provider.buckets);
  donutChart("chart-platform", by.platform.buckets);
  donutChart("chart-severity", by.severity.buckets, (key, i) =>
    SEVERITY_COLORS[key] || PALETTE[i % PALETTE.length],
  );
  donutChart("chart-os", by.os.buckets);
  donutChart("chart-incremental", by.incremental.buckets);

  // Models and versions can have a long tail.
  donutChart("chart-review-model", collapseTail(by.review_model.buckets, 8));
  donutChart("chart-triage-model", collapseTail(by.triage_model.buckets, 8));
  donutChart("chart-version", collapseTail(by.version.buckets, 6));
}

function collapseTail(buckets, keepN) {
  const sorted = buckets.slice().sort((a, b) => b.value - a.value);
  const top = sorted.slice(0, keepN);
  const rest = sorted.slice(keepN);
  if (rest.length) {
    top.push({
      key: `other (${rest.length})`,
      value: rest.reduce((sum, b) => sum + b.value, 0),
    });
  }
  return top;
}

// ---------------------------------------------------------------------
// Filter dropdowns
// ---------------------------------------------------------------------

async function loadFilterOptions() {
  const data = await fetchJSON(`/api/filters?range=${state.range}`);

  const simpleSelect = (key, rows) => {
    const select = document.querySelector(`[data-filter="${key}"]`);
    if (!select) return;
    renderSelectOptions(
      select,
      [
        {
          label: "",
          options: rows.map((r) => ({
            value: r.key,
            text: `${r.key} (${numberFmt.format(r.count)})`,
          })),
        },
      ],
      state.filters[key],
    );
  };

  simpleSelect("platform", data.platforms);
  simpleSelect("provider", data.providers);

  // Models: families first (labelled "All <fam>"), then specific model IDs.
  const modelGroups = (specific, families) => [
    {
      label: "Families",
      options: families.map((f) => ({
        value: `family:${f.key}`,
        text: `All ${f.key} (${numberFmt.format(f.count)})`,
      })),
    },
    {
      label: "Specific models",
      options: specific.map((m) => ({
        value: m.key,
        text: `${m.key} (${numberFmt.format(m.count)})`,
      })),
    },
  ];

  const reviewSelect = document.querySelector('[data-filter="review_model"]');
  if (reviewSelect) {
    renderSelectOptions(
      reviewSelect,
      modelGroups(data.review_models, data.review_model_families || []),
      state.filters.review_model,
    );
  }
  const triageSelect = document.querySelector('[data-filter="triage_model"]');
  if (triageSelect) {
    renderSelectOptions(
      triageSelect,
      modelGroups(data.triage_models, data.triage_model_families || []),
      state.filters.triage_model,
    );
  }

  // Installations: grouped by platform, short git-style hash + count.
  const installSelect = document.querySelector(
    '[data-filter="installation"]',
  );
  if (installSelect) {
    const byPlatform = {};
    for (const row of data.installations) {
      const p = row.platform || "unknown";
      (byPlatform[p] ||= []).push(row);
    }
    const platformOrder = ["github", "local", "unknown"];
    const groups = platformOrder
      .filter((p) => byPlatform[p])
      .map((p) => ({
        label: p === "github" ? "GitHub" : p === "local" ? "Local" : "Other",
        options: byPlatform[p].map((row) => ({
          value: row.key,
          text: `${shortHash(row.key)} · ${numberFmt.format(row.count)}`,
        })),
      }));
    renderSelectOptions(installSelect, groups, state.filters.installation);
  }

  document.getElementById("filter-reset").disabled = !anyFilterActive();
}

// ---------------------------------------------------------------------
// Render
// ---------------------------------------------------------------------

async function render() {
  pushStateToURL();
  clearError();

  document.getElementById("filter-reset").disabled = !anyFilterActive();

  try {
    await Promise.all([loadOverview(), loadTimeseries(), loadBreakdowns()]);
  } catch (err) {
    showError(err);
  }
}

// ---------------------------------------------------------------------
// Wiring
// ---------------------------------------------------------------------

function wireRange() {
  const buttons = document.querySelectorAll(".range button");
  buttons.forEach((btn) => {
    if (btn.dataset.range === state.range) {
      btn.setAttribute("aria-selected", "true");
    } else {
      btn.setAttribute("aria-selected", "false");
    }
    btn.addEventListener("click", async () => {
      buttons.forEach((b) => b.setAttribute("aria-selected", "false"));
      btn.setAttribute("aria-selected", "true");
      state.range = btn.dataset.range;
      // Refresh filter option counts for the new range, then render.
      await loadFilterOptions().catch(showError);
      render();
    });
  });
}

function wireFilters() {
  for (const key of FILTER_KEYS) {
    const select = document.querySelector(`[data-filter="${key}"]`);
    if (!select) continue;
    select.addEventListener("change", () => {
      state.filters[key] = select.value || "";
      render();
    });
  }

  document.getElementById("filter-reset").addEventListener("click", () => {
    if (!anyFilterActive()) return;
    for (const key of FILTER_KEYS) {
      state.filters[key] = "";
      const select = document.querySelector(`[data-filter="${key}"]`);
      if (select) select.value = "";
    }
    render();
  });
}

document.addEventListener("DOMContentLoaded", async () => {
  loadStateFromURL();
  wireRange();
  wireFilters();
  await loadFilterOptions().catch(showError);
  render();
});
