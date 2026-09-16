/* The landing page (overrides/home.html): the release badge, the two sewn
   diagrams, tabs, copy buttons and the scroll reveal. No dependencies. */
(() => {
  'use strict';

  const SVG_NS = 'http://www.w3.org/2000/svg';
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  /* ── the release badge ─────────────────────────────────────────────────── */
  const releaseLabel = document.getElementById('release-label');
  if (releaseLabel) {
    fetch('https://api.github.com/repos/giantswarm/muster/releases/latest', {
      headers: { Accept: 'application/vnd.github+json' },
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((rel) => {
        if (!rel || !rel.tag_name) return;
        const days = Math.floor((Date.now() - new Date(rel.published_at)) / 86400000);
        const when = days < 1 ? 'today' : days === 1 ? 'yesterday' : `${days} days ago`;
        releaseLabel.textContent = `${rel.tag_name} · ${when}`;
      })
      .catch(() => {});
  }

  /* ── reveal on scroll ──────────────────────────────────────────────────── */
  const reveals = document.querySelectorAll('.reveal');
  if (reduced || !('IntersectionObserver' in window)) {
    reveals.forEach((n) => n.classList.add('in'));
  } else {
    const io = new IntersectionObserver((entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); }
      });
    }, { rootMargin: '0px 0px -8% 0px' });
    reveals.forEach((n) => io.observe(n));
  }

  /* ── visibility: an animation runs only while its figure is on screen ── */
  function visibility(target) {
    let intersecting = true;
    let waiters = [];
    const shown = () => intersecting && !document.hidden;
    const release = () => { if (shown()) { waiters.forEach((w) => w()); waiters = []; } };
    if ('IntersectionObserver' in window) {
      new IntersectionObserver((entries) => {
        intersecting = entries[0].isIntersecting;
        release();
      }, { threshold: 0.15 }).observe(target);
    }
    document.addEventListener('visibilitychange', release);
    return () => (shown() ? Promise.resolve() : new Promise((r) => waiters.push(r)));
  }

  /* ── sewing: reveal a stitched path from its start to its end ──────────── */
  let maskSeq = 0;
  function sew(svg, path, ms) {
    const defs = svg.querySelector('defs');
    const id = `sew-${++maskSeq}`;
    const mask = document.createElementNS(SVG_NS, 'mask');
    mask.setAttribute('id', id);
    mask.setAttribute('maskUnits', 'userSpaceOnUse');
    mask.setAttribute('x', '0'); mask.setAttribute('y', '0');
    mask.setAttribute('width', '100%'); mask.setAttribute('height', '100%');
    const cover = document.createElementNS(SVG_NS, 'path');
    cover.setAttribute('d', path.getAttribute('d'));
    cover.setAttribute('pathLength', '100');
    cover.setAttribute('fill', 'none');
    cover.setAttribute('stroke', '#fff');
    cover.setAttribute('stroke-width', '12');
    cover.setAttribute('stroke-linecap', 'round');
    cover.style.strokeDasharray = '100';
    cover.style.strokeDashoffset = reduced ? '0' : '100';
    mask.appendChild(cover);
    defs.appendChild(mask);
    path.setAttribute('mask', `url(#${id})`);
    path.dataset.mask = id;
    if (reduced) return Promise.resolve();
    const anim = cover.animate([{ strokeDashoffset: 100 }, { strokeDashoffset: 0 }], { duration: ms, easing: 'linear', fill: 'forwards' });
    return anim.finished.catch(() => {});
  }
  function unsew(svg, path) {
    const id = path.dataset.mask;
    if (!id) return;
    const mask = svg.querySelector(`#${id}`);
    if (mask) mask.remove();
    path.removeAttribute('mask');
    delete path.dataset.mask;
  }

  /* ── the loom: a thread from a client through muster to a server ──────── */
  const loom = document.getElementById('loom');
  if (loom) {
    const threads = loom.querySelector('#threads');
    const needle = loom.querySelector('#needle');
    const clientY = [88, 188, 288];   // where the client pieces sit
    const entryY = [150, 200, 250];   // where a thread enters muster
    const exitY = [120, 160, 200, 240, 280]; // where it leaves
    const serverY = [58, 130, 202, 274, 346];
    const route = (c, s) =>
      `M204,${clientY[c]} C236,${clientY[c]} 228,${entryY[c]} 260,${entryY[c]} ` +
      `C340,${entryY[c]} 340,${exitY[s]} 420,${exitY[s]} ` +
      `C452,${exitY[s]} 444,${serverY[s]} 476,${serverY[s]}`;
    const routes = [[0, 0], [1, 2], [2, 4], [1, 1], [0, 3], [2, 0]];
    const piece = (name) => loom.querySelector(`[data-piece="${name}"]`);
    const light = (names, on) => names.forEach((n) => piece(n) && piece(n).classList.toggle('lit', on));
    const thread = (d) => {
      const p = document.createElementNS(SVG_NS, 'path');
      p.setAttribute('class', 'thread');
      p.setAttribute('d', d);
      threads.appendChild(p);
      return p;
    };

    if (reduced) {
      routes.slice(0, 3).forEach(([c, s]) => {
        thread(route(c, s));
        light([`c${c + 1}`, `s${s + 1}`], true);
      });
    } else {
      const whenVisible = visibility(loom);
      const followNeedle = (path, ms) => new Promise((resolve) => {
        const total = path.getTotalLength();
        const start = performance.now();
        needle.hidden = false;
        const tick = (now) => {
          const t = Math.min(1, (now - start) / ms);
          const at = path.getPointAtLength(t * total);
          const ahead = path.getPointAtLength(Math.min(total, t * total + 1));
          const angle = Math.atan2(ahead.y - at.y, ahead.x - at.x) * 180 / Math.PI;
          needle.setAttribute('transform', `translate(${at.x} ${at.y}) rotate(${angle})`);
          if (t < 1) requestAnimationFrame(tick); else resolve();
        };
        requestAnimationFrame(tick);
      });
      (async () => {
        let i = 0;
        for (;;) {
          await whenVisible();
          const [c, s] = routes[i % routes.length];
          const p = thread(route(c, s));
          light([`c${c + 1}`], true);
          const ms = 2600;
          sew(loom, p, ms);
          await followNeedle(p, ms);
          light([`s${s + 1}`], true);
          await wait(1100);
          needle.hidden = true;
          p.style.transition = 'opacity 600ms';
          p.style.opacity = '0';
          light([`c${c + 1}`, `s${s + 1}`], false);
          await wait(650);
          unsew(loom, p);
          p.remove();
          await wait(250);
          i++;
        }
      })();
    }
  }

  /* ── the hero terminal: one scene per area, in turn ───────────────────── */
  const scenes = document.getElementById('scenes');
  if (scenes && !reduced) {
    const list = [...scenes.querySelectorAll('.term__scene')];
    const area = document.getElementById('term-area');
    const show = (i) => {
      list.forEach((s, j) => {
        const on = j === i;
        s.classList.toggle('is-active', on);
        s.setAttribute('aria-hidden', String(!on));
      });
      if (area) area.textContent = list[i].dataset.area;
    };
    const whenVisible = visibility(scenes);
    (async () => {
      for (let i = 1; ; i++) {
        await whenVisible();
        await wait(7000);
        await whenVisible();
        show(i % list.length);
      }
    })();
  }

  /* ── the workflow run ──────────────────────────────────────────────────── */
  const wf = document.getElementById('wfsvg');
  if (wf) {
    const node = (id) => wf.querySelector(`[data-node="${id}"]`);
    const edge = (id) => wf.querySelector(`#${id}`);
    const ids = (id) => document.querySelectorAll(`.y-id[data-step="${id}"]`);
    const rows = document.getElementById('rec-rows');
    const recName = document.getElementById('rec-name');
    const recStatus = document.getElementById('rec-status');
    const nodes = ['pods', 'gate', 'events', 'logs', 'tail', 'restarts', 'alerts', 'output', 'note'];
    const edges = ['e0', 'e1', 'e2', 'eskip', 'e3', 'e4', 'e5a', 'e5b', 'e6a', 'e6b', 'e7', 'efail', 'eloop'];

    const setState = (id, state, label) => {
      const n = node(id);
      if (!n) return;
      n.dataset.state = state;
      const badge = n.querySelector(':scope > .badge text');
      if (badge) badge.textContent = label || state;
      ids(id).forEach((s) => s.classList.toggle('lit', state === 'running'));
    };
    const setStatus = (state) => { recStatus.dataset.state = state; recStatus.textContent = state; };
    const row = (id, status, took) => {
      const empty = rows.querySelector('.record__empty');
      if (empty) empty.remove();
      const tr = document.createElement('tr');
      [[id, ''], [status, `st-${status}`], [took || '', 'num']].forEach(([text, cls]) => {
        const td = document.createElement('td');
        td.textContent = text;
        if (cls) td.className = cls;
        tr.appendChild(td);
      });
      rows.appendChild(tr);
      return tr;
    };
    const finishRow = (tr, status, took) => {
      tr.children[1].className = `st-${status}`;
      tr.children[1].textContent = status;
      tr.children[2].textContent = took || '';
    };
    const sewEdge = (id, ms) => {
      const e = edge(id);
      e.classList.add('sewn');
      return sew(wf, e, ms);
    };
    const reset = () => {
      nodes.forEach((id) => { const n = node(id); if (n) { delete n.dataset.state; const b = n.querySelector(':scope > .badge text'); if (b) b.textContent = 'idle'; } });
      edges.forEach((id) => { const e = edge(id); e.classList.remove('sewn'); unsew(wf, e); });
      document.querySelectorAll('.y-id.lit').forEach((s) => s.classList.remove('lit'));
      rows.innerHTML = '<tr class="record__empty"><td colspan="3">waiting for a run…</td></tr>';
      setStatus('idle');
    };
    const newName = () => `pod-triage-${Math.random().toString(36).slice(2, 7)}`;
    const scale = reduced ? 0 : 1;
    const step = async (id, tookMs, took, outcome) => {
      const tr = row(id, 'running');
      setState(id, 'running');
      await wait(tookMs * scale);
      setState(id, outcome || 'completed', outcome === 'failed' ? 'failed' : 'done');
      finishRow(tr, outcome || 'completed', took);
    };

    async function run(kind) {
      recName.textContent = newName();
      setStatus('running');
      await sewEdge('e0', 500 * scale);
      await step('pods', 700, kind === 'skip' ? '388ms · 0 items' : '412ms · 3 items');

      await sewEdge('e1', 450 * scale);
      setState('gate', 'running');
      await wait(450 * scale);
      setState('gate', 'completed');

      if (kind === 'skip') {
        await sewEdge('eskip', 900 * scale);
        setState('events', 'skipped', 'skipped');
        row('events', 'skipped', 'condition false');
      } else {
        await sewEdge('e2', 450 * scale);
        await step('events', 520, '231ms · 41 items');
        await sewEdge('e3', 450 * scale);
      }

      setState('logs', 'running');
      if (kind === 'skip') {
        row('logs', 'completed', '0 iterations');
        setState('logs', 'completed');
      } else {
        for (let i = 0; i < 3; i++) {
          const failed = i === 1;
          const tr = row(`tail_${i}`, 'running');
          setState('tail', 'running');
          await wait(480 * scale);
          setState('tail', failed ? 'failed' : 'completed', failed ? 'failed' : 'done');
          finishRow(tr, failed ? 'failed' : 'completed', failed ? 'allowFailure' : `${160 + i * 23}ms`);
          if (i < 2) {
            const loop = edge('eloop');
            loop.classList.remove('sewn'); unsew(wf, loop);
            await sewEdge('eloop', 380 * scale);
          }
        }
        setState('logs', 'completed');
      }

      await sewEdge('e4', 450 * scale);
      await Promise.all([sewEdge('e5a', 420 * scale), sewEdge('e5b', 420 * scale)]);
      const restartsFails = kind === 'fail';
      await Promise.all([
        step('restarts', 1050, restartsFails ? 'connection refused' : '612ms', restartsFails ? 'failed' : undefined),
        step('alerts', 640, '96ms · 2 firing'),
      ]);

      if (restartsFails) {
        setStatus('failed');
        await sewEdge('efail', 900 * scale);
        await step('note', 600, 'onFailure · 87ms');
        return;
      }
      await Promise.all([sewEdge('e6a', 420 * scale), sewEdge('e6b', 420 * scale)]);
      await sewEdge('e7', 300 * scale);
      setState('output', 'running');
      await wait(300 * scale);
      setState('output', 'completed');
      row('output', 'completed', '3 keys · typed');
      setStatus('completed');
    }

    if (reduced) {
      run('ok');
    } else {
      const whenVisible = visibility(wf);
      (async () => {
        const kinds = ['ok', 'fail', 'skip'];
        for (let i = 0; ; i++) {
          await whenVisible();
          reset();
          await wait(400);
          await run(kinds[i % kinds.length]);
          await wait(3400);
        }
      })();
    }
  }

  /* ── install tabs ──────────────────────────────────────────────────────── */
  const tablist = document.querySelector('[role="tablist"]');
  if (tablist) {
    const tabs = [...tablist.querySelectorAll('[role="tab"]')];
    const select = (tab) => {
      tabs.forEach((t) => {
        const on = t === tab;
        t.setAttribute('aria-selected', String(on));
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute('aria-controls')).hidden = !on;
      });
      tab.focus();
    };
    tabs.forEach((tab, i) => {
      tab.addEventListener('click', () => select(tab));
      tab.addEventListener('keydown', (ev) => {
        const delta = ev.key === 'ArrowRight' ? 1 : ev.key === 'ArrowLeft' ? -1 : 0;
        if (delta) { ev.preventDefault(); select(tabs[(i + delta + tabs.length) % tabs.length]); }
        if (ev.key === 'Home') { ev.preventDefault(); select(tabs[0]); }
        if (ev.key === 'End') { ev.preventDefault(); select(tabs[tabs.length - 1]); }
      });
    });
  }

  /* ── copy buttons ──────────────────────────────────────────────────────── */
  document.querySelectorAll('.copy').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const code = btn.parentElement.querySelector('code');
      try {
        await navigator.clipboard.writeText(code.innerText.replace(/\n$/, ''));
        btn.textContent = 'Copied';
        btn.classList.add('done');
      } catch {
        btn.textContent = 'Select and copy';
      }
      setTimeout(() => { btn.textContent = 'Copy'; btn.classList.remove('done'); }, 1600);
    });
  });
})();
