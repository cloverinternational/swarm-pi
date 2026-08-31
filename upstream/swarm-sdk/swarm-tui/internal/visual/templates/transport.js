(function () {
  'use strict';
  const root = document.getElementById('sw-root');
  const authToken = window.__SW_AUTH__ || '';
  const csrf = window.__SW_CSRF__ || '';

  function q(extra) {
    extra = extra || '';
    if (!authToken) return extra;
    const sep = extra.includes('?') ? '&' : '?';
    return extra + sep + 'k=' + encodeURIComponent(authToken);
  }

  function headers() {
    const h = { 'X-Swarm-CSRF': csrf };
    if (authToken) h['X-Swarm-Auth'] = authToken;
    return h;
  }

  function ensurePrimitive(screen) {
    // Defense-in-depth: guarantee every screen has a .primitive
    // field so renderPrimitive never crashes on `p.kind` when
    // p is undefined. The backend should always provide one,
    // but journal-replayed or stale screens may lack it.
    if (screen.primitive && typeof screen.primitive.kind === 'string') return screen;
    var content = screen.description || screen.title || '(no content)';
    screen.primitive = { kind: 'markdown', content: content };
    return screen;
  }

  const transport = {
    async fetchAll() {
      const r = await fetch('/screens' + q(''));
      if (!r.ok) throw new Error('fetchAll ' + r.status);
      const screens = await r.json();
      if (Array.isArray(screens)) screens.forEach(ensurePrimitive);
      return screens;
    },
    async fetchNewest() {
      const r = await fetch('/newest' + q(''));
      if (r.status === 204) return null;
      if (!r.ok) throw new Error('newest ' + r.status);
      const body = await r.json();
      if (!body.questionID) return null;
      return transport.fetchScreen(body.questionID);
    },
    async fetchScreen(id) {
      const r = await fetch('/screens' + q(''));
      const all = await r.json();
      if (Array.isArray(all)) all.forEach(ensurePrimitive);
      return all.find((s) => s.id === id);
    },
    async submitClick(id, choices) {
      const r = await fetch('/click' + q(''), {
        method: 'POST',
        headers: Object.assign({ 'Content-Type': 'application/json' }, headers()),
        body: JSON.stringify({ questionID: id, choices: choices }),
      });
      if (!r.ok) throw new Error('click ' + r.status);
    },
    async acknowledge(id) {
      const r = await fetch('/ack/' + encodeURIComponent(id) + q(''), {
        method: 'POST', headers: headers(),
      });
      if (!r.ok) throw new Error('ack ' + r.status);
    },
    async reviewPlan(id, decision, note) {
      const r = await fetch('/review/' + encodeURIComponent(id) + q(''), {
        method: 'POST',
        headers: Object.assign({ 'Content-Type': 'application/json' }, headers()),
        body: JSON.stringify({ decision: decision, note: note || '' }),
      });
      if (!r.ok) throw new Error('review ' + r.status);
    },
  };

  window.SwarmVisual.mountCompanion(root, { transport: transport });
})();
