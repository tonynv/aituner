// Builds the aituner website (site/dist) from site/pages: one shared layout, per-page SEO (title, description,
// canonical, Open Graph, Twitter, JSON-LD), sitemap.xml, robots.txt, Cloudflare Pages _headers (with a CSP that hashes
// the one inline script) and a 404 page. No dependencies: node site/build.mjs [SITE_URL]. The site address lives in
// SITE_URL (argument or environment); change it there when the custom domain is attached.
import { createHash } from 'node:crypto';
import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));
const SITE = (process.argv[2] || process.env.SITE_URL || 'https://aituner.app').replace(/\/$/, '');
const EMAIL = 'info@aituner.app';
const REPO = 'https://github.com/tonynv/aituner';
const out = join(root, 'dist');
const today = new Date().toISOString().slice(0, 10);

// ---- pages: each file starts with <!-- {json front matter} --> -------------------------------------------------------
function walk(dir) {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p) : p.endsWith('.html') ? [p] : [];
  });
}
const pages = walk(join(root, 'pages')).map((file) => {
  const raw = readFileSync(file, 'utf8');
  const m = raw.match(/^<!--\s*(\{[\s\S]*?\})\s*-->\s*/);
  if (!m) throw new Error(`${file}: missing front matter`);
  const meta = JSON.parse(m[1]);
  const rel = relative(join(root, 'pages'), file).replace(/\\/g, '/');
  const path = rel === 'index.html' ? '/' : rel === '404.html' ? '/404' : '/' + rel.replace(/index\.html$/, '').replace(/\.html$/, '/');
  for (const k of ['title', 'description']) if (!meta[k]) throw new Error(`${file}: front matter needs ${k}`);
  if (meta.description.length > 170) throw new Error(`${file}: description over 170 characters`);
  return { ...meta, file, rel, path, body: raw.slice(m[0].length) };
});
const docs = pages.filter((p) => p.path.startsWith('/docs/') && p.path !== '/docs/').sort((a, b) => a.order - b.order);

// ---- helpers ----------------------------------------------------------------------------------------------------------
const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const url = (p) => SITE + p;
const jsonld = (o) => `<script type="application/ld+json">${JSON.stringify(o).replace(/</g, '\\u003c')}</script>`;

// the only inline script: set the theme before first paint (no flash); its hash goes into the CSP
const themeInit = `try{var t=localStorage.getItem('aituner-site-theme');if(t)document.documentElement.dataset.theme=t}catch(e){}`;
const themeHash = createHash('sha256').update(themeInit).digest('base64');

const app = {
  '@type': 'SoftwareApplication',
  name: 'aituner',
  applicationCategory: 'DeveloperApplication',
  operatingSystem: 'macOS 14 or later (Apple Silicon)',
  description: 'A native Mac app that detects your Apple Silicon Mac, recommends local AI models that fit it, runs them with MLX, connects your editor and monitors the GPU live.',
  url: SITE + '/',
  downloadUrl: SITE + '/download/',
  image: SITE + '/assets/og.png',
  screenshot: SITE + '/assets/screens/monitor.png',
  softwareRequirements: 'Apple Silicon Mac (M1 or later), macOS 14 or later',
  author: { '@type': 'Organization', name: 'aituner', url: SITE + '/', email: EMAIL, contactPoint: { '@type': 'ContactPoint', email: EMAIL, contactType: 'customer support' } },
};

const nav = [
  ['/#features', 'Features'],
  ['/docs/', 'Docs'],
  ['/download/', 'Download'],
];

function layout(p) {
  const canonical = url(p.path);
  const title = p.path === '/' ? p.title : `${p.title} · aituner`;
  const isDoc = p.path.startsWith('/docs/');
  const crumbs = isDoc && p.path !== '/docs/' ? [['/', 'Home'], ['/docs/', 'Docs'], [p.path, p.nav || p.title]] : isDoc ? [['/', 'Home'], ['/docs/', 'Docs']] : null;
  const ld = [];
  if (p.path === '/') {
    ld.push({ '@context': 'https://schema.org', '@type': 'WebSite', name: 'aituner', url: SITE + '/' });
    ld.push({ '@context': 'https://schema.org', ...app });
  }
  if (p.faq) ld.push({ '@context': 'https://schema.org', '@type': 'FAQPage', mainEntity: p.faq.map(([q, a]) => ({ '@type': 'Question', name: q, acceptedAnswer: { '@type': 'Answer', text: a } })) });
  if (isDoc && p.path !== '/docs/') ld.push({ '@context': 'https://schema.org', '@type': 'TechArticle', headline: p.title, description: p.description, url: canonical, dateModified: today, about: { '@type': 'SoftwareApplication', name: 'aituner' } });
  if (crumbs) ld.push({ '@context': 'https://schema.org', '@type': 'BreadcrumbList', itemListElement: crumbs.map(([u, n], i) => ({ '@type': 'ListItem', position: i + 1, name: n, item: url(u) })) });

  const idx = docs.findIndex((d) => d.path === p.path);
  const prev = idx > 0 ? docs[idx - 1] : null;
  const next = idx >= 0 && idx < docs.length - 1 ? docs[idx + 1] : null;
  const main = isDoc
    ? `<div class="wrap docs">
  <nav class="docnav" aria-label="Documentation">
    <a href="/docs/"${p.path === '/docs/' ? ' aria-current="page"' : ''}>Overview</a>
    ${docs.map((d) => `<a href="${d.path}"${d.path === p.path ? ' aria-current="page"' : ''}>${esc(d.nav || d.title)}</a>`).join('\n    ')}
  </nav>
  <article class="doc">
    ${crumbs ? `<ol class="crumbs">${crumbs.map(([u, n], i) => i < crumbs.length - 1 ? `<li><a href="${u}">${esc(n)}</a></li>` : `<li aria-current="page">${esc(n)}</li>`).join('')}</ol>` : ''}
    ${p.body}
    ${prev || next ? `<nav class="pager" aria-label="Pages">${prev ? `<a class="prev" href="${prev.path}"><span>Previous</span>${esc(prev.nav || prev.title)}</a>` : '<span></span>'}${next ? `<a class="next" href="${next.path}"><span>Next</span>${esc(next.nav || next.title)}</a>` : ''}</nav>` : ''}
  </article>
</div>`
    : p.body;

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>${esc(title)}</title>
<meta name="description" content="${esc(p.description)}">
<link rel="canonical" href="${canonical}">
${p.path === '/404' ? '<meta name="robots" content="noindex">' : '<meta name="robots" content="index, follow">'}
<meta name="color-scheme" content="dark light">
<meta name="theme-color" content="#0a0a0a" media="(prefers-color-scheme: dark)">
<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
<meta property="og:type" content="${p.path === '/' ? 'website' : 'article'}">
<meta property="og:site_name" content="aituner">
<meta property="og:title" content="${esc(title)}">
<meta property="og:description" content="${esc(p.description)}">
<meta property="og:url" content="${canonical}">
<meta property="og:image" content="${SITE}/assets/og.png">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta property="og:image:alt" content="aituner: local AI on your Mac, tuned and running">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="${esc(title)}">
<meta name="twitter:description" content="${esc(p.description)}">
<meta name="twitter:image" content="${SITE}/assets/og.png">
<link rel="icon" href="/assets/icon.svg" type="image/svg+xml">
<link rel="icon" href="/assets/icon-192.png" sizes="192x192" type="image/png">
<link rel="apple-touch-icon" href="/assets/icon-180.png">
<link rel="stylesheet" href="/assets/site.css">
<script>${themeInit}</script>
${ld.map(jsonld).join('\n')}
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<header class="top">
  <div class="wrap">
    <a class="logo" href="/" aria-label="aituner home"><svg class="mark" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="5.5" y="5.5" width="13" height="13"/><path d="M9 2.5v3M15 2.5v3M9 18.5v3M15 18.5v3M2.5 9h3M2.5 15h3M18.5 9h3M18.5 15h3M8 13h2l1.5-3 2 5 1.5-2h1"/></svg>aituner</a>
    <nav class="links" aria-label="Main">
      ${nav.map(([u, n]) => `<a href="${u}"${p.path.startsWith(u) && u !== '/#features' ? ' aria-current="page"' : ''}>${n}</a>`).join('')}
      <a href="${REPO}" rel="noopener">GitHub</a>
    </nav>
    <button class="theme" id="theme" type="button" aria-label="Toggle dark and light theme"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg></button>
    <a class="btn primary small cta" href="/download/">Download</a>
  </div>
</header>
<main id="main">
${main}
</main>
<footer class="foot">
  <div class="wrap">
    <div class="fbrand"><span>aituner</span><p>Local AI on your Mac, tuned and running.</p></div>
    <nav aria-label="Product"><h2>Product</h2><a href="/#features">Features</a><a href="/download/">Download</a><a href="${REPO}/releases">Release notes</a></nav>
    <nav aria-label="Docs"><h2>Docs</h2>${docs.slice(0, 5).map((d) => `<a href="${d.path}">${esc(d.nav || d.title)}</a>`).join('')}</nav>
    <nav aria-label="Project"><h2>Project</h2><a href="${REPO}">Source code</a><a href="${REPO}/issues">Report an issue</a><a href="mailto:${EMAIL}">${EMAIL}</a></nav>
  </div>
</footer>
<script src="/assets/site.js" defer></script>
</body>
</html>
`;
}

// ---- write ------------------------------------------------------------------------------------------------------------
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
cpSync(join(root, 'assets'), join(out, 'assets'), { recursive: true });
for (const f of ['icon.svg', 'icon-180.png', 'icon-192.png']) cpSync(join(root, '..', 'web', 'public', f), join(out, 'assets', f)); // the app's icons: one source
for (const p of pages) {
  const dest = p.path === '/404' ? join(out, '404.html') : join(out, p.path, 'index.html');
  mkdirSync(dirname(dest), { recursive: true });
  writeFileSync(dest, layout(p));
}
const indexed = pages.filter((p) => p.path !== '/404');
writeFileSync(join(out, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${indexed.map((p) => `  <url><loc>${url(p.path)}</loc><lastmod>${today}</lastmod><priority>${p.path === '/' ? '1.0' : p.path === '/download/' ? '0.9' : '0.7'}</priority></url>`).join('\n')}
</urlset>
`);
writeFileSync(join(out, 'robots.txt'), `User-agent: *\nAllow: /\n\nSitemap: ${SITE}/sitemap.xml\n`);
// Cloudflare Pages headers: a strict CSP (the theme script by hash; GitHub's API for the latest release), no framing,
// long caching for images and styles.
writeFileSync(join(out, '_headers'), `/*
  Content-Security-Policy: default-src 'self'; script-src 'self' 'sha256-${themeHash}'; style-src 'self'; img-src 'self' data:; connect-src https://api.github.com; base-uri 'none'; form-action 'none'; frame-ancestors 'none'
  X-Content-Type-Options: nosniff
  Referrer-Policy: strict-origin-when-cross-origin
  Permissions-Policy: camera=(), microphone=(), geolocation=()
  X-Frame-Options: DENY
/assets/*
  Cache-Control: public, max-age=86400
`);
console.log(`built ${pages.length} pages for ${SITE} into ${relative(process.cwd(), out)}`);
