// aituner website: theme toggle, copy buttons, and the latest release (from GitHub's public API) for download links.
(() => {
  const root = document.documentElement;
  document.getElementById('theme')?.addEventListener('click', () => {
    const dark = root.dataset.theme !== 'light'; // dark unless light was chosen
    root.dataset.theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('aituner-site-theme', root.dataset.theme); } catch { /* storage blocked */ }
  });

  for (const b of document.querySelectorAll('[data-copy]')) {
    b.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = 'Copied'; b.classList.add('done'); }
      catch { b.textContent = 'Select and copy'; }
      setTimeout(() => { b.textContent = 'Copy'; b.classList.remove('done'); }, 1600);
    });
  }

  // Elements marked data-release fill in from the latest release; data-prerelease shows while none is published.
  const needs = document.querySelector('[data-release], [data-prerelease]');
  if (!needs) return;
  const set = (sel, fn) => document.querySelectorAll(sel).forEach(fn);
  fetch('https://api.github.com/repos/tonynv/aituner/releases/latest', { headers: { Accept: 'application/vnd.github+json' } })
    .then(async (r) => {
      if (r.status === 404) { set('[data-prerelease]', (e) => (e.hidden = false)); set('[data-release]', (e) => (e.hidden = true)); return; }
      if (!r.ok) return;
      const rel = await r.json();
      const v = rel.tag_name.replace(/^v/, '');
      const asset = (name) => rel.assets.find((a) => a.name === name);
      const dmg = asset(`aituner-${v}.dmg`), zip = asset(`aituner-${v}.zip`);
      set('[data-release]', (e) => (e.hidden = false));
      set('[data-prerelease]', (e) => (e.hidden = true));
      set('[data-version]', (e) => (e.textContent = v));
      set('[data-date]', (e) => (e.textContent = new Date(rel.published_at).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' })));
      set('[data-notes]', (e) => (e.href = rel.html_url));
      if (dmg) {
        set('[data-dmg]', (e) => (e.href = dmg.browser_download_url));
        set('[data-size]', (e) => (e.textContent = `${(dmg.size / 1e6).toFixed(0)} MB`));
        set('[data-sha]', (e) => (e.textContent = dmg.digest ? dmg.digest.replace('sha256:', 'SHA-256 ') : ''));
      }
      if (zip) set('[data-zip]', (e) => (e.href = zip.browser_download_url));
    })
    .catch(() => {});
})();
