// Generates ZeroDesk's built-in wallpapers: original vector art, flat colour
// (no gradients), one light and one dark version per set. Output goes to
// ui/public/wallpapers/<id>-<light|dark>.svg. Every set is drawn from a fixed
// seed, so running this again gives the same files.
//
//   cd design/wallpaper-gen && pnpm install && pnpm build

import { contours } from 'd3-contour';
import { mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const W = 2560;
const H = 1600;
const OUT = fileURLToPath(new URL('../../ui/public/wallpapers/', import.meta.url));

function rng(seed) {
	return () => {
		seed |= 0;
		seed = (seed + 0x6d2b79f5) | 0;
		let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const n = (v) => Math.round(v);

/** A smooth open curve through the points (Catmull-Rom as cubic Béziers). */
function smooth(pts) {
	let d = `M${n(pts[0][0])} ${n(pts[0][1])}`;
	for (let i = 0; i < pts.length - 1; i++) {
		const p0 = pts[i - 1] ?? pts[i];
		const p1 = pts[i];
		const p2 = pts[i + 1];
		const p3 = pts[i + 2] ?? p2;
		const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
		const c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
		d += `C${n(c1[0])} ${n(c1[1])} ${n(c2[0])} ${n(c2[1])} ${n(p2[0])} ${n(p2[1])}`;
	}
	return d;
}

/** The same, closed. */
function smoothClosed(pts) {
	const m = pts.length;
	let d = `M${n(pts[0][0])} ${n(pts[0][1])}`;
	for (let i = 0; i < m; i++) {
		const p0 = pts[(i - 1 + m) % m];
		const p1 = pts[i];
		const p2 = pts[(i + 1) % m];
		const p3 = pts[(i + 2) % m];
		const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
		const c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
		d += `C${n(c1[0])} ${n(c1[1])} ${n(c2[0])} ${n(c2[1])} ${n(p2[0])} ${n(p2[1])}`;
	}
	return d + 'Z';
}

/** A filled band: the curve, then down to the bottom edge. */
const band = (pts) => `${smooth(pts)}L${W + 40} ${H + 40}L-40 ${H + 40}Z`;

function svg(bg, body) {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid slice"><rect width="${W}" height="${H}" fill="${bg}"/>${body}</svg>\n`;
}

// ---- sets ----

/** Wind-shaped dunes under a low sun. */
function dunes(dark) {
	const r = rng(11);
	const sky = dark ? '#1b1815' : '#f2eadd';
	const sun = dark ? '#d9cdb9' : '#e9d5b5';
	const layers = dark ? ['#27221d', '#312a23', '#3b322a', '#463b30', '#524437'] : ['#ead9bf', '#e0c8a6', '#d5b68e', '#c9a378', '#bb9065'];
	let body = `<circle cx="1820" cy="${dark ? 420 : 470}" r="${dark ? 110 : 150}" fill="${sun}"/>`;
	layers.forEach((fill, i) => {
		const base = 760 + i * 170;
		const amp = 90 + i * 30;
		const f = 0.0011 + r() * 0.0006;
		const p = r() * 6.28;
		const pts = [];
		for (let x = -80; x <= W + 80; x += 160) {
			// a sharp-ish crest and a long lee slope
			const s = Math.sin(x * f + p);
			pts.push([x, base - amp * (s > 0 ? s ** 0.7 : s * 0.6) - 30 * Math.sin(x * f * 2.7 + p * 1.3)]);
		}
		body += `<path d="${band(pts)}" fill="${fill}"/>`;
	});
	return svg(sky, body);
}

/** Layered mountain ridges, graphite. */
function ridges(dark) {
	const r = rng(23);
	const sky = dark ? '#111113' : '#ececed';
	const layers = dark ? ['#1b1b1e', '#232326', '#2c2c30', '#36363a', '#414145'] : ['#d9d9db', '#c5c5c8', '#adadb1', '#96969b', '#7e7e83'];
	let body = dark ? `<circle cx="680" cy="380" r="64" fill="#3d3d41"/>` : '';
	layers.forEach((fill, i) => {
		// midpoint displacement gives a mountain silhouette
		let pts = [
			[-80, 700 + i * 150 + r() * 160],
			[W * (0.3 + r() * 0.4), 380 + i * 170 + r() * 120],
			[W + 80, 700 + i * 150 + r() * 160],
		];
		let disp = 300 - i * 30;
		for (let k = 0; k < 6; k++) {
			const next = [pts[0]];
			for (let j = 0; j < pts.length - 1; j++) {
				const a = pts[j];
				const b = pts[j + 1];
				next.push([(a[0] + b[0]) / 2, (a[1] + b[1]) / 2 + (r() - 0.5) * disp], b);
			}
			pts = next;
			disp *= 0.48;
		}
		const d = `M${pts.map((p) => `${n(p[0])} ${n(p[1])}`).join('L')}L${W + 80} ${H + 40}L-80 ${H + 40}Z`;
		body += `<path d="${d}" fill="${fill}"/>`;
	});
	return svg(sky, body);
}

/** Soft rolling hills, sage. */
function meadow(dark) {
	const r = rng(37);
	const sky = dark ? '#141713' : '#eef0e7';
	const sun = dark ? '#c9cfbd' : '#e6e1c8';
	const layers = dark ? ['#1b2019', '#22281f', '#2a3126', '#333b2e', '#3c4536'] : ['#dbe1d1', '#c9d2bc', '#b6c1a7', '#a2af92', '#8e9c7e'];
	let body = `<circle cx="${dark ? 1960 : 640}" cy="${dark ? 400 : 440}" r="${dark ? 96 : 132}" fill="${sun}"/>`;
	layers.forEach((fill, i) => {
		const base = 820 + i * 160;
		const pts = [];
		const f1 = 0.0009 + r() * 0.0005;
		const f2 = 0.0023 + r() * 0.001;
		const p1 = r() * 6.28;
		const p2 = r() * 6.28;
		for (let x = -80; x <= W + 200; x += 200) {
			pts.push([x, base - (90 + i * 10) * Math.sin(x * f1 + p1) - 40 * Math.sin(x * f2 + p2)]);
		}
		body += `<path d="${band(pts)}" fill="${fill}"/>`;
	});
	return svg(sky, body);
}

/** Topographic contour lines on paper. */
function topo(dark) {
	const r = rng(51);
	const gw = 96;
	const gh = 60;
	const s = W / gw;
	const peaks = Array.from({ length: 9 }, () => [r() * gw, r() * gh, 6 + r() * 14, (r() < 0.25 ? -0.7 : 1) * (0.6 + r() * 0.6)]);
	const waves = Array.from({ length: 4 }, () => [0.05 + r() * 0.1, 0.05 + r() * 0.1, r() * 6.28, r() * 6.28]);
	const values = new Float64Array(gw * gh);
	for (let y = 0; y < gh; y++) {
		for (let x = 0; x < gw; x++) {
			let v = 0;
			for (const [px, py, rad, h] of peaks) v += h * Math.exp(-((x - px) ** 2 + (y - py) ** 2) / (2 * rad * rad));
			for (const [fx, fy, a, b] of waves) v += 0.12 * Math.sin(x * fx + a) * Math.cos(y * fy + b);
			values[y * gw + x] = v;
		}
	}
	const levels = Array.from({ length: 20 }, (_, i) => -0.6 + i * 0.09);
	const paper = dark ? '#1b1a18' : '#efebe4';
	const line = dark ? '#36332e' : '#d3cbbf';
	const major = dark ? '#4a453e' : '#bcb1a2';
	let body = '';
	contours()
		.size([gw, gh])
		.thresholds(levels)(values)
		.forEach((c, i) => {
			let d = '';
			for (const poly of c.coordinates) {
				for (const ring of poly) {
					// drop the closing point and every other one (the curve
					// smooths them back), skip specks
					const pts = ring
						.slice(0, -1)
						.filter((_, k) => k % 2 === 0)
						.map(([x, y]) => [x * s, y * s]);
					if (pts.length < 5) continue;
					d += smoothClosed(pts);
				}
			}
			if (d) body += `<path d="${d}" fill="none" stroke="${i % 5 === 0 ? major : line}" stroke-width="${i % 5 === 0 ? 3 : 2}"/>`;
		});
	return svg(paper, body);
}

/** Large overlapping discs, clay and rose. */
function circles(dark) {
	const bg = dark ? '#1e1716' : '#f1e5df';
	const fills = dark ? ['#2b201e', '#352724', '#41302b', '#4d3932'] : ['#e7cdc3', '#ddb9ab', '#d2a593', '#c4907c'];
	const discs = [
		[520, 1420, 920],
		[2060, 220, 860],
		[1560, 1360, 640],
		[980, 420, 420],
	];
	const body = discs.map(([cx, cy, rr], i) => `<circle cx="${cx}" cy="${cy}" r="${rr}" fill="${fills[i]}" fill-opacity="0.85"/>`).join('');
	return svg(bg, body);
}

/** The CardputerZero's keyboard, tone on tone, with the red power key. */
function keys(dark) {
	const bg = dark ? '#18181a' : '#cbcccf';
	const key = dark ? '#222225' : '#bdbec2';
	const red = '#d6453a';
	const size = 190;
	const gap = 46;
	const step = size + gap;
	let body = `<g transform="rotate(-14 ${W / 2} ${H / 2})">`;
	for (let row = -3; row < 10; row++) {
		const off = (row % 2) * (step / 2);
		for (let col = -3; col < 15; col++) {
			const x = col * step + off - 120;
			const y = row * step - 140;
			const fill = row === 4 && col === 7 ? red : key;
			body += `<rect x="${x}" y="${y}" width="${size}" height="${size}" rx="34" fill="${fill}"/>`;
		}
	}
	body += '</g>';
	return svg(bg, body);
}

const SETS = { dunes, ridges, meadow, topo, circles, keys };

mkdirSync(OUT, { recursive: true });
for (const [id, make] of Object.entries(SETS)) {
	for (const scheme of ['light', 'dark']) {
		const file = `${id}-${scheme}.svg`;
		const data = make(scheme === 'dark');
		writeFileSync(OUT + file, data);
		console.log(`${file.padEnd(20)} ${(data.length / 1024).toFixed(1)} KB`);
	}
}
