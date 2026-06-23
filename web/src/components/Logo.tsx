// 8's mark — deliberately NOT the digit. Two loops meeting at an empty waist:
// the top loop is the CALL physics (blue), the bottom is the CHANNEL physics
// (purple). Tilted 45° and cast from an offset ghost, so it reads as projected
// from another plane rather than drawn flat. The center stays empty — the wire
// is a passage, not a node.
export function Logo8() {
  return (
    <div className="logo8-wrap" title="8 — the intersection of the two wires">
      <svg className="logo8" viewBox="0 0 48 48" width="40" height="40" aria-label="8">
        <g transform="rotate(45 24 24)">
          <g className="logo8-ghost" transform="translate(4 4)">
            <circle cx="24" cy="16" r="8" />
            <circle cx="24" cy="31" r="8" />
          </g>
          <circle className="logo8-call" cx="24" cy="16" r="8" />
          <circle className="logo8-chan" cx="24" cy="31" r="8" />
        </g>
      </svg>
    </div>
  );
}
