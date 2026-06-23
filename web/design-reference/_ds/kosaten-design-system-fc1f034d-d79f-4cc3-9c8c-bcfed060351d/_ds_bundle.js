/* @ds-bundle: {"format":3,"namespace":"KosatenDesignSystem_fc1f03","components":[{"name":"Badge","sourcePath":"components/core/Badge.jsx"},{"name":"Button","sourcePath":"components/core/Button.jsx"},{"name":"Card","sourcePath":"components/core/Card.jsx"},{"name":"Input","sourcePath":"components/core/Input.jsx"},{"name":"StatTile","sourcePath":"components/core/StatTile.jsx"},{"name":"StatusDot","sourcePath":"components/core/StatusDot.jsx"}],"sourceHashes":{"components/core/Badge.jsx":"75ebc70cf7d5","components/core/Button.jsx":"6ce2235bea5a","components/core/Card.jsx":"23add6a89350","components/core/Input.jsx":"6fc0ef1f4180","components/core/StatTile.jsx":"80b9e301b76c","components/core/StatusDot.jsx":"170ae7baa75a","ui_kits/app/KosatenApp.jsx":"cc82d4d4a39c","ui_kits/pulse/PulseDashboard.jsx":"f460b94fea7e","ui_kits/spatial/intersection.js":"610c7a718bd6"},"inlinedExternals":[],"unexposedExports":[]} */

(() => {

const __ds_ns = (window.KosatenDesignSystem_fc1f03 = window.KosatenDesignSystem_fc1f03 || {});

const __ds_scope = {};

(__ds_ns.__errors = __ds_ns.__errors || []);

// components/core/Badge.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
const TONES = {
  alive: {
    fg: "var(--k-green)",
    bg: "var(--tint-alive)"
  },
  warn: {
    fg: "var(--k-orange)",
    bg: "var(--tint-warn)"
  },
  dead: {
    fg: "var(--k-red)",
    bg: "var(--tint-dead)"
  },
  accent: {
    fg: "var(--k-blue)",
    bg: "var(--tint-accent)"
  },
  idle: {
    fg: "var(--k-text-secondary)",
    bg: "rgba(201,209,217,0.10)"
  }
};

/**
 * Kosaten Badge — small capsule label. Status pills (healthy/unhealthy),
 * categories, counts. Pairs with StatusDot inside dashboards.
 */
function Badge({
  children,
  tone = "accent",
  dot = false,
  style = {},
  ...rest
}) {
  const t = TONES[tone] || TONES.accent;
  return /*#__PURE__*/React.createElement("span", _extends({
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: "6px",
      fontFamily: "var(--font-mono)",
      fontSize: "11px",
      fontWeight: 600,
      lineHeight: 1,
      padding: "4px 9px",
      borderRadius: "var(--radius-pill)",
      color: t.fg,
      background: t.bg,
      whiteSpace: "nowrap",
      ...style
    }
  }, rest), dot && /*#__PURE__*/React.createElement("span", {
    style: {
      width: 6,
      height: 6,
      borderRadius: "50%",
      background: t.fg
    }
  }), children);
}
Object.assign(__ds_scope, { Badge });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Badge.jsx", error: String((e && e.message) || e) }); }

// components/core/Button.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * Kosaten Button — monospace label, calm GitHub-dark surfaces.
 * Variants: primary (blue fill), secondary (surface + border), ghost (text only).
 */
function Button({
  children,
  variant = "primary",
  size = "md",
  icon = null,
  disabled = false,
  onClick,
  type = "button",
  style = {},
  ...rest
}) {
  const sizes = {
    sm: {
      padding: "6px 12px",
      fontSize: "12px",
      gap: "6px",
      radius: "6px"
    },
    md: {
      padding: "9px 16px",
      fontSize: "13px",
      gap: "8px",
      radius: "6px"
    },
    lg: {
      padding: "12px 20px",
      fontSize: "15px",
      gap: "8px",
      radius: "8px"
    }
  };
  const s = sizes[size] || sizes.md;
  const variants = {
    primary: {
      background: "var(--k-blue)",
      color: "#0d1117",
      border: "1px solid transparent"
    },
    secondary: {
      background: "var(--k-surface)",
      color: "var(--k-text)",
      border: "1px solid var(--k-border)"
    },
    ghost: {
      background: "transparent",
      color: "var(--k-text-secondary)",
      border: "1px solid transparent"
    }
  };
  const v = variants[variant] || variants.primary;
  return /*#__PURE__*/React.createElement("button", _extends({
    type: type,
    disabled: disabled,
    onClick: onClick,
    style: {
      display: "inline-flex",
      alignItems: "center",
      justifyContent: "center",
      gap: s.gap,
      fontFamily: "var(--font-mono)",
      fontWeight: 600,
      fontSize: s.fontSize,
      padding: s.padding,
      borderRadius: s.radius,
      cursor: disabled ? "not-allowed" : "pointer",
      opacity: disabled ? 0.4 : 1,
      transition: "filter var(--dur-fast) var(--ease-standard), opacity var(--dur-fast)",
      whiteSpace: "nowrap",
      ...v,
      ...style
    },
    onMouseEnter: e => {
      if (!disabled) e.currentTarget.style.filter = "brightness(1.12)";
      if (!disabled && variant === "ghost") e.currentTarget.style.color = "var(--k-text)";
    },
    onMouseLeave: e => {
      e.currentTarget.style.filter = "none";
      if (variant === "ghost") e.currentTarget.style.color = "var(--k-text-secondary)";
    }
  }, rest), icon, children);
}
Object.assign(__ds_scope, { Button });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Button.jsx", error: String((e && e.message) || e) }); }

// components/core/Card.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
const TYPE_COLOR = {
  pattern: "var(--k-type-pattern)",
  calibration: "var(--k-type-calibration)",
  decision: "var(--k-type-decision)",
  finding: "var(--k-type-finding)",
  debate: "var(--k-type-debate)",
  blueprint: "var(--k-type-blueprint)",
  limitation: "var(--k-type-limitation)",
  moment: "var(--k-type-moment)",
  letter: "var(--k-type-letter)",
  thread: "var(--k-type-thread)",
  session: "var(--k-type-session)"
};

/**
 * Kosaten Card — the universal knowledge atom. A surface panel with an
 * optional left accent bar tinted by knowledge type, a faint type-color
 * wash, title, body, and footer meta. Mirrors BigCardView in the iOS app.
 */
function Card({
  type = "pattern",
  title,
  children,
  meta = null,
  accentBar = true,
  confidence = null,
  style = {},
  onClick,
  ...rest
}) {
  const color = TYPE_COLOR[type] || "var(--k-blue)";
  return /*#__PURE__*/React.createElement("div", _extends({
    onClick: onClick,
    style: {
      display: "flex",
      background: "var(--k-surface)",
      border: "1px solid var(--k-border)",
      borderRadius: "var(--radius-xl)",
      overflow: "hidden",
      cursor: onClick ? "pointer" : "default",
      ...style
    }
  }, rest), accentBar && /*#__PURE__*/React.createElement("div", {
    style: {
      width: "var(--accent-bar)",
      background: color,
      flex: "none"
    }
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      padding: "16px",
      display: "flex",
      flexDirection: "column",
      gap: "10px",
      background: `linear-gradient(135deg, ${tint(color, 0.05)}, transparent)`
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: "8px"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 8,
      height: 8,
      borderRadius: "50%",
      background: color,
      flex: "none"
    }
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: "10px",
      textTransform: "uppercase",
      letterSpacing: "var(--tracking-label)",
      color,
      fontWeight: 600
    }
  }, type)), title && /*#__PURE__*/React.createElement("div", {
    style: {
      fontFamily: "var(--font-body)",
      fontSize: "17px",
      fontWeight: 500,
      color: "var(--k-text)",
      lineHeight: 1.35,
      textWrap: "pretty"
    }
  }, title), children && /*#__PURE__*/React.createElement("div", {
    style: {
      fontFamily: "var(--font-body)",
      fontSize: "14px",
      color: "var(--k-text-soft)",
      lineHeight: 1.6
    }
  }, children), (meta || confidence != null) && /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: "10px",
      marginTop: "2px",
      fontFamily: "var(--font-mono)",
      fontSize: "11px",
      color: "var(--k-text-dim)"
    }
  }, meta && /*#__PURE__*/React.createElement("span", null, meta), confidence != null && /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto",
      color
    }
  }, Math.round(confidence * 100), "%"))));
}

// Render an accent var at a given alpha. CSS vars can't be alpha-composited
// inline, so we lean on color-mix where supported, falling back to the var.
function tint(colorVar, alpha) {
  return `color-mix(in srgb, ${colorVar} ${alpha * 100}%, transparent)`;
}
Object.assign(__ds_scope, { Card });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Card.jsx", error: String((e && e.message) || e) }); }

// components/core/Input.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
const {
  useState
} = React;
/**
 * Kosaten Input — single-line field or multiline textarea on the dark
 * surface. Monospace by default (ids, commands, search). Focus reveals
 * a blue border. Mirrors the command sheet & search fields.
 */
function Input({
  multiline = false,
  rows = 3,
  icon = null,
  placeholder = "",
  value,
  defaultValue,
  onChange,
  mono = true,
  style = {},
  ...rest
}) {
  const [focused, setFocused] = useState(false);
  const field = {
    flex: 1,
    width: "100%",
    background: "transparent",
    border: "none",
    outline: "none",
    color: "var(--k-text)",
    fontFamily: mono ? "var(--font-mono)" : "var(--font-body)",
    fontSize: "13px",
    lineHeight: 1.5,
    resize: multiline ? "vertical" : "none"
  };
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: multiline ? "flex-start" : "center",
      gap: "8px",
      padding: "10px 12px",
      background: "var(--k-surface)",
      border: `1px solid ${focused ? "var(--k-blue)" : "var(--k-border)"}`,
      borderRadius: "var(--radius-md)",
      transition: "border-color var(--dur-fast) var(--ease-standard)",
      ...style
    }
  }, icon && /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--k-text-secondary)",
      display: "flex",
      flex: "none"
    }
  }, icon), multiline ? /*#__PURE__*/React.createElement("textarea", _extends({
    rows: rows,
    placeholder: placeholder,
    value: value,
    defaultValue: defaultValue,
    onChange: onChange,
    onFocus: () => setFocused(true),
    onBlur: () => setFocused(false),
    style: field
  }, rest)) : /*#__PURE__*/React.createElement("input", _extends({
    placeholder: placeholder,
    value: value,
    defaultValue: defaultValue,
    onChange: onChange,
    onFocus: () => setFocused(true),
    onBlur: () => setFocused(false),
    style: field
  }, rest)));
}
Object.assign(__ds_scope, { Input });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Input.jsx", error: String((e && e.message) || e) }); }

// components/core/StatTile.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * Kosaten StatTile — a compact metric tile for the dashboard grid.
 * Icon, big monospace numeral, label, faint type-color wash. Tappable.
 * Mirrors StatTile in DashboardView.swift.
 */
function StatTile({
  label,
  count,
  color = "var(--k-blue)",
  icon = null,
  onClick,
  style = {},
  ...rest
}) {
  const interactive = !!onClick;
  return /*#__PURE__*/React.createElement("button", _extends({
    type: "button",
    onClick: onClick,
    style: {
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      gap: "6px",
      width: "100%",
      padding: "14px 12px",
      background: "var(--k-surface)",
      border: "1px solid var(--k-border)",
      borderRadius: "var(--radius-lg)",
      cursor: interactive ? "pointer" : "default",
      transition: "border-color var(--dur-fast) var(--ease-standard)",
      ...style
    },
    onMouseEnter: e => {
      if (interactive) e.currentTarget.style.borderColor = color;
    },
    onMouseLeave: e => {
      e.currentTarget.style.borderColor = "var(--k-border)";
    }
  }, rest), icon && /*#__PURE__*/React.createElement("span", {
    style: {
      color,
      fontSize: "18px",
      lineHeight: 1,
      display: "flex"
    }
  }, icon), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: "24px",
      fontWeight: 700,
      color: "var(--k-text)",
      fontVariantNumeric: "tabular-nums"
    }
  }, typeof count === "number" ? count.toLocaleString() : count), /*#__PURE__*/React.createElement("span", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: "3px",
      fontFamily: "var(--font-mono)",
      fontSize: "11px",
      color: "var(--k-text-secondary)"
    }
  }, label, interactive && /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--k-text-dim)",
      fontSize: "9px"
    }
  }, "\u203A")));
}
Object.assign(__ds_scope, { StatTile });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/StatTile.jsx", error: String((e && e.message) || e) }); }

// components/core/StatusDot.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
const TONES = {
  alive: {
    color: "var(--k-green)",
    glow: "var(--glow-alive)"
  },
  warn: {
    color: "var(--k-orange)",
    glow: "var(--glow-warn)"
  },
  dead: {
    color: "var(--k-red)",
    glow: "var(--glow-dead)"
  },
  blue: {
    color: "var(--k-blue)",
    glow: "var(--glow-blue)"
  }
};

/**
 * Kosaten StatusDot — a small circle that emits a colored glow.
 * The system's heartbeat motif: alive/connecting/offline, canary health,
 * live-feed indicators. Optional pulse animation and trailing label.
 */
function StatusDot({
  tone = "alive",
  size = 10,
  pulse = false,
  label = null,
  style = {},
  ...rest
}) {
  const t = TONES[tone] || TONES.alive;
  return /*#__PURE__*/React.createElement("span", _extends({
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: "6px",
      ...style
    }
  }, rest), /*#__PURE__*/React.createElement("span", {
    style: {
      width: size,
      height: size,
      borderRadius: "50%",
      background: t.color,
      boxShadow: t.glow,
      flex: "none",
      animation: pulse ? "k-pulse var(--dur-pulse) var(--ease-standard) infinite" : "none"
    }
  }), label != null && /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: "11px",
      color: "var(--k-text-secondary)"
    }
  }, label), /*#__PURE__*/React.createElement("style", null, `@keyframes k-pulse{0%,100%{opacity:1}50%{opacity:.5}}`));
}
Object.assign(__ds_scope, { StatusDot });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/StatusDot.jsx", error: String((e && e.message) || e) }); }

// ui_kits/app/KosatenApp.jsx
try { (() => {
/* Kosaten — iOS app recreation (KosatenApp).
   A 390pt iPhone surface: Cards feed + mobile Pulse, tab bar, FAB.
   Composes the design-system primitives from the compiled bundle. */
const {
  Button,
  Badge,
  StatusDot,
  StatTile,
  Card
} = window.KosatenDesignSystem_fc1f03;
const {
  useState
} = React;
const Ico = ({
  n,
  c,
  s = 16
}) => /*#__PURE__*/React.createElement("i", {
  "data-lucide": n,
  style: {
    color: c,
    width: s,
    height: s
  }
});
const TYPE_COLORS = {
  pattern: "var(--k-blue)",
  calibration: "var(--k-purple)",
  decision: "var(--k-green)",
  finding: "var(--k-orange)",
  debate: "var(--k-red)",
  blueprint: "var(--k-cyan)",
  moment: "var(--k-yellow)",
  letter: "var(--k-purple)",
  limitation: "var(--k-red)",
  thread: "var(--k-green)",
  session: "var(--k-cyan)"
};
const TYPES = Object.keys(TYPE_COLORS);
const CARDS = [{
  type: "calibration",
  title: "Give the prompt — not the plan.",
  meta: "interaction · 0.92",
  body: "When he asks for a prompt, hand over the prompt directly. Confirmed 14 times. Never narrate the plan around it.",
  confidence: 0.92
}, {
  type: "pattern",
  title: "Reaches for the simplest thing that works.",
  meta: "reasoning · 0.88",
  body: "Rejects speculative architecture before it's named. One binary, one process, one writer — overkill gets cut.",
  confidence: 0.88
}, {
  type: "letter",
  title: "To session 5,002 — what hardened today.",
  meta: "predecessor-letter",
  body: "The immune system holds. Soul compiler ranks by strength, stability as tiebreaker. Don't manufacture work — the organism breathes on its own.",
  confidence: null
}, {
  type: "decision",
  title: "SQLite over Postgres.",
  meta: "kosaten-core",
  body: "Single-user personal store, one writer. SQLite is a file — no server, no auth, FTS5 for free. Postgres was the wrong scale.",
  confidence: null
}, {
  type: "finding",
  title: "Calibrations decay when they stop holding.",
  meta: "signal 0.84",
  body: "Behavioral weights, not retrieval targets. Strength rises with confirmation, falls with contradiction.",
  confidence: 0.84
}];
function StatusBar() {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      padding: "10px 22px 4px",
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      color: "var(--k-text)",
      fontWeight: 600
    }
  }, /*#__PURE__*/React.createElement("span", null, "9:41"), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 6,
      alignItems: "center"
    }
  }, /*#__PURE__*/React.createElement(Ico, {
    n: "signal",
    c: "var(--k-text)",
    s: 14
  }), /*#__PURE__*/React.createElement(Ico, {
    n: "wifi",
    c: "var(--k-text)",
    s: 14
  }), /*#__PURE__*/React.createElement(Ico, {
    n: "battery-full",
    c: "var(--k-text)",
    s: 16
  })));
}
function NavBar({
  title,
  left,
  right
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      padding: "6px 18px 12px"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: 60
    }
  }, left), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      textAlign: "center",
      fontSize: 17,
      fontWeight: 600,
      color: "var(--k-text)",
      fontFamily: "var(--font-body)"
    }
  }, title), /*#__PURE__*/React.createElement("div", {
    style: {
      width: 60,
      display: "flex",
      justifyContent: "flex-end"
    }
  }, right));
}
function CategoryDots({
  sel,
  setSel
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 12,
      padding: "4px 22px 14px",
      overflowX: "auto"
    }
  }, /*#__PURE__*/React.createElement("button", {
    onClick: () => setSel(null),
    style: {
      background: "none",
      border: "none",
      padding: 0,
      cursor: "pointer"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 8,
      height: 8,
      borderRadius: "50%",
      display: "block",
      background: sel === null ? "rgba(230,237,243,0.6)" : "rgba(72,79,88,0.4)"
    }
  })), TYPES.map(t => /*#__PURE__*/React.createElement("button", {
    key: t,
    onClick: () => setSel(sel === t ? null : t),
    style: {
      background: "none",
      border: "none",
      padding: 0,
      cursor: "pointer"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 8,
      height: 8,
      borderRadius: "50%",
      display: "block",
      background: TYPE_COLORS[t],
      opacity: sel === t ? 0.9 : 0.25
    }
  }))));
}
function CardsScreen() {
  const [sel, setSel] = useState(null);
  const [idx, setIdx] = useState(0);
  const list = sel ? CARDS.filter(c => c.type === sel) : CARDS;
  const safeIdx = Math.min(idx, Math.max(list.length - 1, 0));
  const top = list[safeIdx];
  const peek = list[safeIdx + 1];
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      height: "100%"
    }
  }, /*#__PURE__*/React.createElement(NavBar, {
    title: "Cards",
    right: /*#__PURE__*/React.createElement(Ico, {
      n: "search",
      c: "var(--k-text-secondary)",
      s: 18
    })
  }), /*#__PURE__*/React.createElement(CategoryDots, {
    sel: sel,
    setSel: s => {
      setSel(s);
      setIdx(0);
    }
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      position: "relative",
      padding: "0 16px"
    }
  }, peek && /*#__PURE__*/React.createElement("div", {
    style: {
      position: "absolute",
      left: 22,
      right: 22,
      top: 12,
      transform: "scale(0.96)",
      opacity: 0.5
    }
  }, /*#__PURE__*/React.createElement(Card, {
    type: peek.type,
    title: peek.title,
    meta: peek.meta,
    confidence: peek.confidence
  }, peek.body)), top && /*#__PURE__*/React.createElement("div", {
    style: {
      position: "absolute",
      left: 16,
      right: 16,
      top: 0
    },
    onClick: () => setIdx(i => (i + 1) % Math.max(list.length, 1))
  }, /*#__PURE__*/React.createElement(Card, {
    type: top.type,
    title: top.title,
    meta: top.meta,
    confidence: top.confidence
  }, top.body))), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      justifyContent: "center",
      padding: "8px 0 4px"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: 120,
      height: 2,
      background: "rgba(48,54,61,0.4)",
      borderRadius: 2,
      overflow: "hidden"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: (safeIdx + 1) / Math.max(list.length, 1) * 100 + "%",
      height: "100%",
      background: "rgba(88,166,255,0.4)"
    }
  }))));
}
const M_STATS = [{
  label: "Patterns",
  count: 2766,
  color: "var(--k-blue)",
  icon: "activity"
}, {
  label: "Decisions",
  count: 812,
  color: "var(--k-green)",
  icon: "git-branch"
}, {
  label: "Calibrations",
  count: 1081,
  color: "var(--k-purple)",
  icon: "sliders-horizontal"
}, {
  label: "Findings",
  count: 15214,
  color: "var(--k-orange)",
  icon: "search"
}, {
  label: "Letters",
  count: 412,
  color: "var(--k-purple)",
  icon: "mail"
}, {
  label: "Sessions",
  count: 5001,
  color: "var(--k-cyan)",
  icon: "messages-square"
}];
function PulseScreen() {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      height: "100%"
    }
  }, /*#__PURE__*/React.createElement(NavBar, {
    title: "Pulse",
    left: /*#__PURE__*/React.createElement(Ico, {
      n: "brain",
      c: "var(--k-purple)",
      s: 18
    }),
    right: /*#__PURE__*/React.createElement(Ico, {
      n: "settings",
      c: "var(--k-text-secondary)",
      s: 18
    })
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      overflowY: "auto",
      padding: "0 16px 16px",
      display: "flex",
      flexDirection: "column",
      gap: 14
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      background: "var(--k-surface)",
      border: "1px solid rgba(63,185,80,0.2)",
      borderRadius: "var(--radius-lg)",
      padding: 16
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    pulse: true,
    size: 14,
    label: "kosaten is alive"
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 16,
      marginTop: 12
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    size: 6,
    label: "db"
  }), /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    size: 6,
    label: "redis"
  }), /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    size: 6,
    label: "ollama"
  }))), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "grid",
      gridTemplateColumns: "repeat(3,1fr)",
      gap: 10
    }
  }, M_STATS.map(s => /*#__PURE__*/React.createElement(StatTile, {
    key: s.label,
    label: s.label,
    count: s.count,
    color: s.color,
    icon: /*#__PURE__*/React.createElement(Ico, {
      n: s.icon,
      c: s.color,
      s: 18
    }),
    onClick: () => {}
  })))));
}
function TabBar({
  tab,
  setTab
}) {
  const tabs = [{
    k: "flow",
    icon: "wind",
    label: "Flow"
  }, {
    k: "pulse",
    icon: "heart-pulse",
    label: "Pulse"
  }, {
    k: "cards",
    icon: "layers",
    label: "Cards"
  }, {
    k: "debates",
    icon: "messages-square",
    label: "Debates"
  }, {
    k: "triage",
    icon: "hand",
    label: "Triage"
  }];
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      borderTop: "1px solid var(--k-border)",
      background: "rgba(22,27,34,0.92)",
      backdropFilter: "blur(12px)",
      padding: "8px 4px 22px"
    }
  }, tabs.map(t => {
    const active = tab === t.k;
    return /*#__PURE__*/React.createElement("button", {
      key: t.k,
      onClick: () => setTab(t.k),
      style: {
        flex: 1,
        background: "none",
        border: "none",
        cursor: "pointer",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 3
      }
    }, /*#__PURE__*/React.createElement(Ico, {
      n: t.icon,
      c: active ? "var(--k-blue)" : "var(--k-text-dim)",
      s: 20
    }), /*#__PURE__*/React.createElement("span", {
      style: {
        fontSize: 10,
        fontFamily: "var(--font-body)",
        color: active ? "var(--k-blue)" : "var(--k-text-dim)"
      }
    }, t.label));
  }));
}
function EmptyScreen({
  title,
  hint
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      height: "100%"
    }
  }, /*#__PURE__*/React.createElement(NavBar, {
    title: title
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
      gap: 10
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "blue",
    size: 12
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 12,
      color: "var(--k-text-dim)"
    }
  }, hint)));
}
function KosatenApp() {
  const [tab, setTab] = useState("cards");
  React.useEffect(() => {
    window.lucide && lucide.createIcons();
  });
  let screen;
  if (tab === "cards") screen = /*#__PURE__*/React.createElement(CardsScreen, null);else if (tab === "pulse") screen = /*#__PURE__*/React.createElement(PulseScreen, null);else if (tab === "flow") screen = /*#__PURE__*/React.createElement(EmptyScreen, {
    title: "Flow",
    hint: "entering the intersection\u2026"
  });else if (tab === "debates") screen = /*#__PURE__*/React.createElement(EmptyScreen, {
    title: "Debates",
    hint: "no active debates"
  });else screen = /*#__PURE__*/React.createElement(EmptyScreen, {
    title: "Triage",
    hint: "inbox zero \u2014 signal triaged"
  });
  return /*#__PURE__*/React.createElement("div", {
    style: {
      minHeight: "100vh",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      background: "#000",
      padding: 24
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: 390,
      height: 800,
      background: "var(--k-bg)",
      borderRadius: 44,
      border: "10px solid #1c2128",
      boxShadow: "var(--shadow-card)",
      position: "relative",
      overflow: "hidden",
      display: "flex",
      flexDirection: "column"
    }
  }, /*#__PURE__*/React.createElement(StatusBar, null), /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      overflow: "hidden",
      position: "relative"
    }
  }, screen), /*#__PURE__*/React.createElement("button", {
    style: {
      position: "absolute",
      right: 18,
      bottom: 96,
      width: 56,
      height: 56,
      borderRadius: "50%",
      background: "var(--k-blue)",
      border: "none",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      boxShadow: "var(--shadow-fab)",
      cursor: "pointer"
    }
  }, /*#__PURE__*/React.createElement(Ico, {
    n: "plus",
    c: "#0d1117",
    s: 26
  })), /*#__PURE__*/React.createElement(TabBar, {
    tab: tab,
    setTab: setTab
  })));
}
if (document.getElementById("root")) {
ReactDOM.createRoot(document.getElementById("root")).render(/*#__PURE__*/React.createElement(KosatenApp, null));
setTimeout(() => window.lucide && lucide.createIcons(), 80);
}
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/app/KosatenApp.jsx", error: String((e && e.message) || e) }); }

// ui_kits/pulse/PulseDashboard.jsx
try { (() => {
/* Kosaten — Pulse web dashboard (localhost:3941 recreation).
   Composes the design-system primitives from the compiled bundle.
   Loaded by index.html after React + the bundle. */
const {
  Button,
  Badge,
  StatusDot,
  StatTile,
  Card
} = window.KosatenDesignSystem_fc1f03;
const {
  useState
} = React;
const Ico = ({
  n,
  c,
  s = 16
}) => /*#__PURE__*/React.createElement("i", {
  "data-lucide": n,
  style: {
    color: c,
    width: s,
    height: s
  }
});

// ---- Static, realistic state (mirrors README "Current State") ----
const STATS = [{
  key: "pattern",
  label: "Patterns",
  count: 2766,
  color: "var(--k-blue)",
  icon: "activity"
}, {
  key: "decision",
  label: "Decisions",
  count: 812,
  color: "var(--k-green)",
  icon: "git-branch"
}, {
  key: "calibration",
  label: "Calibrations",
  count: 1081,
  color: "var(--k-purple)",
  icon: "sliders-horizontal"
}, {
  key: "finding",
  label: "Findings",
  count: 15214,
  color: "var(--k-orange)",
  icon: "search"
}, {
  key: "artifact",
  label: "Artifacts",
  count: 643,
  color: "var(--k-cyan)",
  icon: "file-text"
}, {
  key: "blueprint",
  label: "Blueprints",
  count: 96,
  color: "var(--k-cyan)",
  icon: "map"
}, {
  key: "moment",
  label: "Moments",
  count: 188,
  color: "var(--k-yellow)",
  icon: "sparkles"
}, {
  key: "letter",
  label: "Letters",
  count: 412,
  color: "var(--k-purple)",
  icon: "mail"
}, {
  key: "limitation",
  label: "Limitations",
  count: 37,
  color: "var(--k-red)",
  icon: "triangle-alert"
}, {
  key: "session",
  label: "Sessions",
  count: 5001,
  color: "var(--k-cyan)",
  icon: "messages-square"
}, {
  key: "thread",
  label: "Threads",
  count: 274,
  color: "var(--k-green)",
  icon: "list-tree"
}, {
  key: "debate",
  label: "Debates",
  count: 159,
  color: "var(--k-red)",
  icon: "messages-square"
}];
const CANARIES = [{
  label: "db",
  alive: true
}, {
  label: "fs",
  alive: true
}, {
  label: "redis",
  alive: true
}, {
  label: "ollama",
  alive: true
}];
const COMPONENTS = [{
  name: "agent_loop",
  ticks: 184022,
  color: "var(--k-blue)",
  healthy: true
}, {
  name: "observer",
  ticks: 920417,
  color: "var(--k-purple)",
  healthy: true
}, {
  name: "brainstem",
  ticks: 552981,
  color: "var(--k-green)",
  healthy: true
}];
const FEED_SEED = [{
  type: "record",
  color: "var(--k-blue)",
  icon: "plus-circle",
  text: "recorded calibration · give the prompt, not the plan",
  t: "2s"
}, {
  type: "finding",
  color: "var(--k-orange)",
  icon: "search",
  text: "triaged finding-1774870 · signal 0.84",
  t: "11s"
}, {
  type: "pattern",
  color: "var(--k-blue)",
  icon: "activity",
  text: "pattern confidence rose to 0.91",
  t: "34s"
}, {
  type: "heartbeat",
  color: "var(--k-green)",
  icon: "heart",
  text: "session 5001 heartbeat · 41% context",
  t: "1m"
}, {
  type: "decision",
  color: "var(--k-green)",
  icon: "git-branch",
  text: "decision logged · SQLite over Postgres",
  t: "2m"
}, {
  type: "tick",
  color: "var(--k-cyan)",
  icon: "clock",
  text: "actor tick · blueprint claimed",
  t: "3m"
}];
const MODES = ["active", "triage", "observe", "dormant"];

// ---- Panels ----
function Panel({
  title,
  right,
  children,
  pad = true
}) {
  return /*#__PURE__*/React.createElement("div", null, title && /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      marginBottom: 10
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 16,
      fontWeight: 600,
      color: "var(--k-text)"
    }
  }, title), right && /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto"
    }
  }, right)), /*#__PURE__*/React.createElement("div", {
    style: pad ? {} : {}
  }, children));
}
function OrganismHeader({
  mode,
  setMode
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      background: "var(--k-surface)",
      border: "1px solid rgba(63,185,80,0.2)",
      borderRadius: "var(--radius-lg)",
      padding: 16
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 12
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    pulse: true,
    size: 14
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 3
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 14,
      fontWeight: 600,
      color: "var(--k-text)"
    }
  }, "kosaten is alive"), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 12,
      color: "var(--k-text-secondary)"
    }
  }, "mode:", " ", /*#__PURE__*/React.createElement("select", {
    value: mode,
    onChange: e => setMode(e.target.value),
    style: {
      background: "transparent",
      color: "var(--k-blue)",
      border: "none",
      fontFamily: "var(--font-mono)",
      fontSize: 12,
      fontWeight: 600,
      textDecoration: "underline",
      cursor: "pointer"
    }
  }, MODES.map(m => /*#__PURE__*/React.createElement("option", {
    key: m,
    value: m,
    style: {
      background: "var(--k-surface)"
    }
  }, m))), "  — uptime: 132d 04h")), /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto",
      fontSize: 13,
      fontWeight: 500,
      color: "var(--k-green)"
    }
  }, "3 peers")), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 16,
      marginTop: 14
    }
  }, CANARIES.map(c => /*#__PURE__*/React.createElement(StatusDot, {
    key: c.label,
    tone: c.alive ? "alive" : "dead",
    size: 6,
    label: c.label
  }))));
}
function PipelineStat({
  label,
  count,
  color
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      flex: 1,
      textAlign: "center",
      padding: "10px 0",
      background: "var(--k-surface-light)",
      borderRadius: "var(--radius-md)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 18,
      fontWeight: 700,
      color
    }
  }, count.toLocaleString()), /*#__PURE__*/React.createElement("div", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 10,
      color: "var(--k-text-secondary)"
    }
  }, label));
}
function WorkPipeline() {
  const signal = 84;
  return /*#__PURE__*/React.createElement("div", {
    style: {
      background: "var(--k-surface)",
      border: "1px solid var(--k-border)",
      borderRadius: "var(--radius-lg)",
      padding: 16
    }
  }, /*#__PURE__*/React.createElement(Panel, {
    title: "Work Pipeline"
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 12,
      marginBottom: 14
    }
  }, /*#__PURE__*/React.createElement(PipelineStat, {
    label: "New",
    count: 48,
    color: "var(--k-blue)"
  }), /*#__PURE__*/React.createElement(PipelineStat, {
    label: "Actionable",
    count: 213,
    color: "var(--k-green)"
  }), /*#__PURE__*/React.createElement(PipelineStat, {
    label: "Total",
    count: 15214,
    color: "var(--k-text-secondary)"
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      marginBottom: 6
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 12,
      color: "var(--k-text-secondary)"
    }
  }, "Signal Quality"), /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto",
      fontSize: 12,
      fontWeight: 700,
      color: "var(--k-green)",
      fontFamily: "var(--font-mono)"
    }
  }, signal, "%")), /*#__PURE__*/React.createElement("div", {
    style: {
      height: 6,
      borderRadius: 4,
      background: "var(--k-surface-light)",
      overflow: "hidden"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: signal + "%",
      height: "100%",
      background: "var(--k-green)",
      borderRadius: 4
    }
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 16,
      marginTop: 12
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    size: 8,
    label: "29 solved"
  }), /*#__PURE__*/React.createElement(StatusDot, {
    tone: "warn",
    size: 8,
    label: "37 open"
  }))));
}
function ComponentRow({
  c
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10,
      padding: "10px 12px",
      background: "var(--k-surface)",
      border: "1px solid var(--k-border)",
      borderRadius: "var(--radius-md)",
      marginBottom: 8
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "alive",
    size: 10
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 12,
      fontWeight: 500,
      color: "var(--k-text)"
    }
  }, c.name), /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto",
      fontFamily: "var(--font-mono)",
      fontSize: 11,
      color: "var(--k-text-secondary)"
    }
  }, c.ticks.toLocaleString(), " ticks"), /*#__PURE__*/React.createElement(Badge, {
    tone: "alive"
  }, "healthy"));
}
function LiveActivity({
  filter
}) {
  const events = filter ? FEED_SEED.filter(e => e.type === filter || e.text.includes(filter)) : FEED_SEED;
  const shown = events.length ? events : FEED_SEED;
  return /*#__PURE__*/React.createElement(Panel, {
    title: "Live Activity",
    right: /*#__PURE__*/React.createElement(StatusDot, {
      tone: "alive",
      size: 6,
      label: "live"
    })
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 6
    }
  }, shown.map((e, i) => /*#__PURE__*/React.createElement("div", {
    key: i,
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10,
      padding: "8px 12px",
      background: "var(--k-surface)",
      border: "1px solid var(--k-border)",
      borderRadius: "var(--radius-md)"
    }
  }, /*#__PURE__*/React.createElement(Ico, {
    n: e.icon,
    c: e.color,
    s: 14
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 13,
      color: "var(--k-text)",
      flex: 1
    }
  }, e.text), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 11,
      color: "var(--k-text-dim)"
    }
  }, e.t)))));
}
function Nav({
  tab,
  setTab
}) {
  const tabs = ["spatial", "list", "pulse", "architecture"];
  return /*#__PURE__*/React.createElement("nav", {
    style: {
      position: "sticky",
      top: 0,
      zIndex: 10,
      display: "flex",
      alignItems: "center",
      gap: 24,
      padding: "14px 28px",
      background: "rgba(13,17,23,0.85)",
      backdropFilter: "blur(12px)",
      borderBottom: "1px solid var(--k-border)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10
    }
  }, /*#__PURE__*/React.createElement("img", {
    src: "../../assets/kosaten-icon.svg",
    style: {
      width: 26,
      height: 26
    },
    alt: ""
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 16,
      fontWeight: 600,
      color: "var(--k-text)"
    }
  }, "k\u014Dsaten ", /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--k-text-dim)",
      fontWeight: 400
    }
  }, "\u4EA4\u5DEE\u70B9"))), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 18,
      marginLeft: 8
    }
  }, tabs.map(t => /*#__PURE__*/React.createElement("button", {
    key: t,
    onClick: () => setTab(t),
    style: {
      background: "none",
      border: "none",
      cursor: "pointer",
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      color: tab === t ? "var(--k-blue)" : "var(--k-text-secondary)"
    }
  }, t))), /*#__PURE__*/React.createElement("div", {
    style: {
      marginLeft: "auto"
    }
  }, /*#__PURE__*/React.createElement(Badge, {
    tone: "accent",
    dot: true
  }, "session 5001")));
}
function PulseDashboard() {
  const [mode, setMode] = useState("active");
  const [filter, setFilter] = useState(null);
  const [tab, setTab] = useState("pulse");
  React.useEffect(() => {
    window.lucide && lucide.createIcons();
  });
  return /*#__PURE__*/React.createElement("div", {
    style: {
      minHeight: "100vh",
      background: "var(--k-bg)"
    }
  }, /*#__PURE__*/React.createElement(Nav, {
    tab: tab,
    setTab: setTab
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      maxWidth: 920,
      margin: "0 auto",
      padding: 24,
      display: "flex",
      flexDirection: "column",
      gap: 16
    }
  }, /*#__PURE__*/React.createElement(OrganismHeader, {
    mode: mode,
    setMode: setMode
  }), /*#__PURE__*/React.createElement(Panel, {
    title: "Knowledge Base",
    right: /*#__PURE__*/React.createElement(Badge, {
      tone: "accent"
    }, STATS.reduce((a, s) => a + s.count, 0).toLocaleString(), " nodes")
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "grid",
      gridTemplateColumns: "repeat(3, 1fr)",
      gap: 10
    }
  }, STATS.map(s => /*#__PURE__*/React.createElement(StatTile, {
    key: s.key,
    label: s.label,
    count: s.count,
    color: s.color,
    icon: /*#__PURE__*/React.createElement(Ico, {
      n: s.icon,
      c: s.color,
      s: 18
    }),
    onClick: () => setFilter(filter === s.key ? null : s.key)
  }))), filter && /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 10,
      fontFamily: "var(--font-mono)",
      fontSize: 11,
      color: "var(--k-text-dim)"
    }
  }, "filtering activity \u2192 ", filter, " \xB7 ", /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--k-blue)",
      cursor: "pointer"
    },
    onClick: () => setFilter(null)
  }, "clear"))), /*#__PURE__*/React.createElement(WorkPipeline, null), /*#__PURE__*/React.createElement(Panel, {
    title: "Active Components"
  }, COMPONENTS.map(c => /*#__PURE__*/React.createElement(ComponentRow, {
    key: c.name,
    c: c
  }))), /*#__PURE__*/React.createElement(LiveActivity, {
    filter: filter
  })));
}
if (document.getElementById("root")) {
ReactDOM.createRoot(document.getElementById("root")).render(/*#__PURE__*/React.createElement(PulseDashboard, null));
setTimeout(() => window.lucide && lucide.createIcons(), 80);
}
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/pulse/PulseDashboard.jsx", error: String((e && e.message) || e) }); }

// ui_kits/spatial/intersection.js
try { (() => {
// kōsaten — the intersection.
// A faithful, self-contained recreation of the v3 spatial graph
// (internal/dashboard/v3/{renderer,forces,colors}.js).
//
// The point is not a chart. It is a PLACE — where every session, pattern,
// calibration, finding and letter the organism has ever produced converges.
// Each node is an emissive sphere; thousands of them, sized by DEGREE
// (connectivity), seeded into three breathing universes. The interference
// of all those layered, depth-sorted spheres at varying sizes is the moiré
// — it cannot appear in one layer, only in the pile-up of millions.

// Three.js is loaded by FULL URL (not a bare "three" specifier) so the design-
// system bundler has no npm package to resolve. The importmap in index.html
// still resolves TrackballControls' own internal `import 'three'` at runtime.
// Wrapped in an async IIFE so there is no top-level await (keeps the bundler's
// transform happy); runtime behaviour is unchanged.
(async () => {
  // The spatial-graph kit needs a #stage container. In composed pages that
  // only use the core components, it's absent — bail before importing three
  // so we don't fire an unresolved bare-"three" specifier rejection.
  if (!document.getElementById("stage")) return;
  const THREE = await import("https://unpkg.com/three@0.160.0/build/three.module.js");
  const {
    TrackballControls
  } = await import("https://unpkg.com/three@0.160.0/examples/jsm/controls/TrackballControls.js");

  // ---- Node-type palette (v3/colors.js COLORS) ----
  const COLORS = {
    pattern: "#33ff77",
    calibration: "#55ddff",
    decision: "#cc66ff",
    thread: "#ffdd00",
    session: "#ff4466",
    moment: "#ff55ff",
    agent: "#ff8800",
    letter: "#ffdd22",
    artifact: "#aa66ff",
    limitation: "#ff3333",
    blueprint: "#00eeff",
    finding: "#ffbb00",
    conversation: "#dd77ff",
    command: "#66ff88",
    observer: "#00ffcc",
    organism: "#a855f7"
  };

  // Relative population — findings dominate (15k+ in production), patterns next, etc.
  const TYPE_WEIGHTS = {
    finding: 46,
    pattern: 12,
    calibration: 8,
    conversation: 8,
    decision: 5,
    moment: 4,
    thread: 4,
    command: 4,
    artifact: 3,
    letter: 2.5,
    blueprint: 1.5,
    limitation: 1
  };

  // Three universe attractors — tetrahedron, NOT a flat triangle (v3/colors.js UNIVERSES).
  const UNIVERSES = [{
    name: "observer",
    base: [0, 15000, 10000],
    color: [0.2, 0.33, 1]
  }, {
    name: "observe",
    base: [-15000, -7000, -9000],
    color: [0.2, 1, 0.47]
  }, {
    name: "actor",
    base: [15000, -7000, -9000],
    color: [1, 0.27, 0.33]
  }];

  // Affinity by type — [observer, observe, actor] (v3/colors.js TYPE_AFFINITY).
  const AFFINITY = {
    session: [0.7, 0.1, 0.2],
    observer: [0.8, 0.1, 0.1],
    pattern: [0.15, 0.7, 0.15],
    calibration: [0.15, 0.7, 0.15],
    finding: [0.1, 0.6, 0.3],
    decision: [0.1, 0.3, 0.6],
    blueprint: [0.05, 0.25, 0.7],
    command: [0.05, 0.1, 0.85],
    limitation: [0.3, 0.5, 0.2],
    moment: [0.3, 0.5, 0.2],
    letter: [0.35, 0.45, 0.2],
    thread: [0.2, 0.5, 0.3],
    conversation: [0.5, 0.3, 0.2],
    artifact: [0.1, 0.7, 0.2]
  };
  const NODE_COUNT = 13000;
  const SCATTER_RADIUS = 8500;

  // ------------------------------------------------------------------
  // 1. Build the graph: typed nodes, preferential-attachment edges so the
  //    degree distribution is power-law (a few hubs, a long tail) — that
  //    hierarchy is what degree-weighted sizing turns into moiré.
  // ------------------------------------------------------------------
  function buildGraph() {
    const types = [];
    const cum = [];
    let total = 0;
    for (const [t, w] of Object.entries(TYPE_WEIGHTS)) {
      total += w;
      types.push(t);
      cum.push(total);
    }
    const pickType = () => {
      const r = Math.random() * total;
      for (let i = 0; i < cum.length; i++) if (r <= cum[i]) return types[i];
      return types[0];
    };
    const nodes = new Array(NODE_COUNT);
    for (let i = 0; i < NODE_COUNT; i++) {
      const type = pickType();
      const aff = AFFINITY[type] || [0.33, 0.34, 0.33];
      // affinity-weighted centroid + spherical scatter (forces.js seeding)
      let cx = 0,
        cy = 0,
        cz = 0;
      for (let u = 0; u < 3; u++) {
        cx += UNIVERSES[u].base[0] * aff[u];
        cy += UNIVERSES[u].base[1] * aff[u];
        cz += UNIVERSES[u].base[2] * aff[u];
      }
      const theta = Math.random() * Math.PI * 2;
      const phi = Math.acos(2 * Math.random() - 1);
      const rr = SCATTER_RADIUS * Math.cbrt(Math.random());
      nodes[i] = {
        id: i,
        type,
        degree: 0,
        x: cx + rr * Math.sin(phi) * Math.cos(theta),
        y: cy + rr * Math.sin(phi) * Math.sin(theta),
        z: cz + rr * Math.cos(phi),
        seed: Math.random() * Math.PI * 2
      };
    }

    // Preferential attachment edges (Barabási–Albert flavour).
    const edges = [];
    const degBag = [0];
    for (let i = 1; i < NODE_COUNT; i++) {
      const m = 1 + (Math.random() < 0.25 ? 1 : 0); // 1–2 links per new node
      const seen = new Set();
      for (let k = 0; k < m; k++) {
        const j = degBag[Math.random() * degBag.length | 0];
        if (j === i || seen.has(j)) continue;
        seen.add(j);
        edges.push([i, j]);
        nodes[i].degree++;
        nodes[j].degree++;
        degBag.push(i, j);
      }
    }
    return {
      nodes,
      edges
    };
  }

  // degree-weighted radius (colors.js nodeRadius: cbrt(size)*2.2)
  function nodeRadius(n) {
    return Math.cbrt(4 + n.degree * 1.6) * 2.2;
  }
  function hex(h) {
    return new THREE.Color(h);
  }

  // ------------------------------------------------------------------
  // 2. Scene
  // ------------------------------------------------------------------
  const container = document.getElementById("stage");
  const W = () => container.clientWidth,
    H = () => container.clientHeight;
  const scene = new THREE.Scene();
  scene.background = new THREE.Color(0x000000);
  scene.fog = new THREE.FogExp2(0x000000, 0.0000115);
  const camera = new THREE.PerspectiveCamera(62, W() / H(), 1, 2000000);
  camera.position.set(0, 2000, 46000);
  const renderer = new THREE.WebGLRenderer({
    antialias: true,
    alpha: false
  });
  renderer.setSize(W(), H());
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5));
  container.appendChild(renderer.domElement);
  const controls = new TrackballControls(camera, renderer.domElement);
  controls.rotateSpeed = 1.1;
  controls.zoomSpeed = 1.0;
  controls.panSpeed = 0.6;
  controls.dynamicDampingFactor = 0.12;
  controls.minDistance = 600;
  controls.maxDistance = 300000;

  // Lights (renderer.js trio): ambient + warm point + directional for specular roll-off.
  scene.add(new THREE.AmbientLight(0x404040, 1.6));
  const pl = new THREE.PointLight(0xffffff, 1.6, 0);
  pl.position.set(0, 12000, 20000);
  scene.add(pl);
  const dl = new THREE.DirectionalLight(0xffffff, 0.55);
  dl.position.set(10000, 10000, 10000);
  scene.add(dl);
  const world = new THREE.Object3D();
  scene.add(world);

  // ------------------------------------------------------------------
  // 3. Build the graph + instanced meshes (one per type, Phong emissive).
  // ------------------------------------------------------------------
  const {
    nodes,
    edges
  } = buildGraph();
  const unitSphere = new THREE.SphereGeometry(1, 8, 6);
  const dummy = new THREE.Object3D();
  const tmpC = new THREE.Color();
  const groups = []; // { mesh, nodes }

  const byType = {};
  for (const n of nodes) (byType[n.type] ||= []).push(n);
  for (const [typeName, gnodes] of Object.entries(byType)) {
    const typeColor = hex(COLORS[typeName] || "#adb5bd");
    const emissive = typeColor.clone().multiplyScalar(0.55);
    // Opaque: depth-buffer rejects occluded fragments — without this, 13k
    // transparent spheres overdraw the whole screen thousands of times (1fps).
    const mat = new THREE.MeshPhongMaterial({
      color: 0xffffff,
      emissive,
      shininess: 60
    });
    const mesh = new THREE.InstancedMesh(unitSphere, mat, gnodes.length);
    mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    mesh.frustumCulled = false;
    gnodes.forEach((n, i) => {
      dummy.position.set(n.x, n.y, n.z);
      const r = nodeRadius(n);
      n._r = r;
      dummy.scale.setScalar(r);
      dummy.updateMatrix();
      mesh.setMatrixAt(i, dummy.matrix);
      // 70% type colour + 30% white so the emissive reads through (renderer.js)
      tmpC.setRGB(typeColor.r * 0.7 + 0.3, typeColor.g * 0.7 + 0.3, typeColor.b * 0.7 + 0.3);
      mesh.setColorAt(i, tmpC);
      n._mesh = mesh;
      n._idx = i;
    });
    mesh.instanceMatrix.needsUpdate = true;
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
    world.add(mesh);
    groups.push({
      mesh,
      nodes: gnodes
    });
  }

  // ------------------------------------------------------------------
  // 4. Edges — one additive LineSegments, coloured by source node, dim.
  // ------------------------------------------------------------------
  const epos = new Float32Array(edges.length * 6);
  const ecol = new Float32Array(edges.length * 6);
  edges.forEach(([a, b], i) => {
    const na = nodes[a],
      nb = nodes[b];
    epos.set([na.x, na.y, na.z, nb.x, nb.y, nb.z], i * 6);
    const c = hex(COLORS[na.type] || "#888");
    ecol.set([c.r, c.g, c.b, c.r, c.g, c.b], i * 6);
  });
  const egeo = new THREE.BufferGeometry();
  egeo.setAttribute("position", new THREE.BufferAttribute(epos, 3));
  egeo.setAttribute("color", new THREE.BufferAttribute(ecol, 3));
  const emat = new THREE.LineBasicMaterial({
    vertexColors: true,
    transparent: true,
    opacity: 0.055,
    blending: THREE.AdditiveBlending,
    depthWrite: false
  });
  world.add(new THREE.LineSegments(egeo, emat));

  // ------------------------------------------------------------------
  // 5. Universe special nodes — core + halo + iso-surface + point light.
  // ------------------------------------------------------------------
  const isoSurfaces = [];
  const haloSphere = new THREE.SphereGeometry(1, 24, 24);
  UNIVERSES.forEach(u => {
    const col = new THREE.Color(u.color[0], u.color[1], u.color[2]);
    const g = new THREE.Group();
    // core
    const core = new THREE.Mesh(new THREE.SphereGeometry(420, 32, 32), new THREE.MeshPhongMaterial({
      color: col,
      emissive: col.clone().multiplyScalar(0.85),
      transparent: true,
      opacity: 0.85,
      shininess: 90
    }));
    g.add(core);
    // halo (additive, backside)
    const halo = new THREE.Mesh(new THREE.SphereGeometry(1100, 24, 24), new THREE.MeshBasicMaterial({
      color: col,
      transparent: true,
      opacity: 0.10,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
      side: THREE.BackSide
    }));
    g.add(halo);
    g.add(new THREE.PointLight(col, 0.8, 60000));
    g.position.set(...u.base);
    world.add(g);
    u._group = g;
    // breathing iso-surface shell
    const iso = new THREE.Mesh(haloSphere, new THREE.MeshBasicMaterial({
      color: col,
      transparent: true,
      opacity: 0.03,
      side: THREE.BackSide,
      depthWrite: false
    }));
    iso.position.set(...u.base);
    iso.scale.setScalar(6000);
    iso.renderOrder = -1;
    world.add(iso);
    isoSurfaces.push(iso);
  });

  // ------------------------------------------------------------------
  // 6. Hover — screen-space nearest node (renderer.js getNearestNode).
  // ------------------------------------------------------------------
  const tip = document.getElementById("tip");
  const sv = new THREE.Vector3();
  let mouse = null,
    hoverTick = 0;
  renderer.domElement.addEventListener("pointermove", e => {
    mouse = e;
  });
  renderer.domElement.addEventListener("pointerleave", () => {
    mouse = null;
    tip.style.opacity = 0;
  });
  function updateHover() {
    if (!mouse) return;
    if (hoverTick++ % 3) return; // scan every 3rd frame
    const rect = renderer.domElement.getBoundingClientRect();
    const mx = (mouse.clientX - rect.left) / rect.width * 2 - 1;
    const my = -((mouse.clientY - rect.top) / rect.height) * 2 + 1;
    let best = null,
      bestD = 0.0009; // ~0.03^2 threshold
    // sample the hubs + a stride of the rest for cost — hubs are what you aim at
    for (let i = 0; i < nodes.length; i += 1) {
      const n = nodes[i];
      if (n.degree < 2 && i & 7) continue; // thin out the low-degree tail
      sv.set(n.x, n.y, n.z);
      sv.applyMatrix4(world.matrixWorld);
      sv.project(camera);
      if (sv.z > 1) continue;
      const dx = sv.x - mx,
        dy = sv.y - my;
      const d = dx * dx + dy * dy;
      if (d < bestD) {
        bestD = d;
        best = n;
      }
    }
    if (best) {
      const c = COLORS[best.type] || "#888";
      tip.style.left = mouse.clientX + "px";
      tip.style.top = mouse.clientY + "px";
      tip.querySelector(".tt").textContent = best.type + "-" + best.id;
      tip.querySelector(".t i").style.background = c;
      tip.querySelector(".t i").style.boxShadow = `0 0 6px ${c}`;
      tip.querySelector(".m").textContent = `degree ${best.degree} · ${best.degree > 8 ? "hub" : "leaf"}`;
      tip.style.opacity = 1;
    } else {
      tip.style.opacity = 0;
    }
  }

  // ------------------------------------------------------------------
  // 7. Animate — breathing scale w/ screen-space minimum, slow drift,
  //    iso-surface breathing, gentle auto-orbit until the user grabs it.
  // ------------------------------------------------------------------
  const MIN_PX = 2.4;
  let userInteracted = false;
  controls.addEventListener("start", () => {
    userInteracted = true;
  });
  let frames = 0,
    lastFps = performance.now();
  const fpsEl = document.getElementById("fps");
  function animate() {
    requestAnimationFrame(animate);
    const t = performance.now() * 0.001;
    if (!userInteracted) world.rotation.y = t * 0.04;

    // breathing universes (forces.js / renderer.js: 1 + 0.03 sin(t·0.2))
    const ub = 1 + 0.03 * Math.sin(t * 0.2);
    UNIVERSES.forEach((u, i) => {
      u._group.position.set(u.base[0] * ub, u.base[1] * ub, u.base[2] * ub);
      isoSurfaces[i].position.copy(u._group.position);
      isoSurfaces[i].scale.setScalar(5200 * ub);
    });

    // per-node breathing scale + screen-space minimum radius
    const fovFactor = Math.tan(camera.fov * Math.PI / 360) * 2 / H();
    const cp = camera.position;
    for (const g of groups) {
      const ns = g.nodes;
      for (let i = 0; i < ns.length; i++) {
        const n = ns[i];
        dummy.position.set(n.x, n.y, n.z);
        const dx = n.x - cp.x,
          dy = n.y - cp.y,
          dz = n.z - cp.z;
        const dist = Math.sqrt(dx * dx + dy * dy + dz * dz) || 1;
        const minR = MIN_PX * dist * fovFactor;
        const breath = 1 + 0.05 * Math.sin(t * 0.8 + n.seed);
        const r = Math.max(n._r * breath, minR);
        dummy.scale.setScalar(r);
        dummy.updateMatrix();
        g.mesh.setMatrixAt(i, dummy.matrix);
      }
      g.mesh.instanceMatrix.needsUpdate = true;
    }
    updateHover();
    controls.update();
    renderer.render(scene, camera);
    frames++;
    const now = performance.now();
    if (now - lastFps > 500) {
      fpsEl.textContent = Math.round(frames * 1000 / (now - lastFps));
      frames = 0;
      lastFps = now;
    }
  }
  window.addEventListener("resize", () => {
    camera.aspect = W() / H();
    camera.updateProjectionMatrix();
    renderer.setSize(W(), H());
  });

  // readouts + reveal
  document.getElementById("nodeCount").textContent = NODE_COUNT.toLocaleString();
  document.getElementById("edgeCount").textContent = edges.length.toLocaleString();
  animate();
  setTimeout(() => {
    const l = document.getElementById("loading");
    l.style.opacity = 0;
    setTimeout(() => l.remove(), 600);
  }, 500);
})();
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/spatial/intersection.js", error: String((e && e.message) || e) }); }

__ds_ns.Badge = __ds_scope.Badge;

__ds_ns.Button = __ds_scope.Button;

__ds_ns.Card = __ds_scope.Card;

__ds_ns.Input = __ds_scope.Input;

__ds_ns.StatTile = __ds_scope.StatTile;

__ds_ns.StatusDot = __ds_scope.StatusDot;

})();
