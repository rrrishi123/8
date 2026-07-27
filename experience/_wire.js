// _wire.js — shared browser helper for the experience demo.
//
//   WIRE.base()  = the COLLECTOR target for http/sse/mjpeg (swappable; default =
//                  this origin = the demo witness). Set via the index base field.
//   WIRE.fire()  = report that this host fired something. It ALWAYS posts to this
//                  origin (the server that served the page), so no matter where
//                  you point the target, the fire lands in your local witness.
//
// This is the seam that makes "any host fires → 8 sees it automatically" real,
// and the same seam that lets the site point at a real omarchy collector or a
// deployed wire without changing a line of page code.
window.WIRE = {
  base(){ return (localStorage.getItem('wire.base') || location.origin).replace(/\/$/, ''); },
  setBase(b){ b ? localStorage.setItem('wire.base', b.replace(/\/$/, '')) : localStorage.removeItem('wire.base'); },
  async fire(who, what, mode){
    const t = Date.now();
    try{
      const r = await fetch(location.origin + '/fire', {
        method: 'POST', headers: {'Content-Type':'application/json'},
        body: JSON.stringify({ who, what, mode, t }),
      });
      return await r.json();
    }catch(e){ return { ok:false, err:e.message }; }
  },
};
