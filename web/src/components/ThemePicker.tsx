import { useState } from 'react';
import { applyTheme } from '../lib/theme';

// bottom-right background-color chooser; the palette + ink recolor from one pick.
const SWATCHES = ['#000000', '#0b0e14', '#0d1117', '#101216', '#161616', '#0a0f0a', '#15110d', '#1a1430', '#f8f8f8'];

export function ThemePicker({ initial }: { initial: string }) {
  const [open, setOpen] = useState(false);
  const [bg, setBg] = useState(initial);
  const pick = (c: string) => { setBg(c); applyTheme(c); };
  return (
    <div className="theme-pick">
      {open && (
        <div className="theme-pop">
          <div className="sw-row">
            {SWATCHES.map((c) => (
              <button key={c} className={`sw${c === bg ? ' on' : ''}`} style={{ background: c }} onClick={() => pick(c)} title={c} />
            ))}
          </div>
          <label className="theme-custom">custom
            <input type="color" value={/^#[0-9a-fA-F]{6}$/.test(bg) ? bg : '#000000'} onChange={(e) => pick(e.target.value)} />
          </label>
        </div>
      )}
      <button className="theme-btn" title="background color" onClick={() => setOpen((o) => !o)} style={{ background: bg }} />
    </div>
  );
}
