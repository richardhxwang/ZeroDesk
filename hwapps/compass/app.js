'use strict';

const t = zd.localize({
	en: {
		title: 'Compass', calibrate: 'Calibrate', sensors: 'Sensors', pitch: 'Pitch', roll: 'Roll',
		accel: 'Accelerometer', gyro: 'Gyroscope', mag: 'Magnetometer', calibration: 'Calibration',
		calibrated: 'Calibrated', notCalibrated: 'Not calibrated', calTitle: 'Calibrate Compass',
		rotate: 'Turn CardputerZero slowly through every direction', turn: 'Turn it over and rotate all sides',
		metal: 'Move away from metal and magnets', save: 'Could not save the calibration', done: 'Done',
		calDone: 'Compass calibrated', cancel: 'Cancel', noMag: 'No magnetometer', noImu: 'Motion sensor unavailable',
		dirs: 'N,NE,E,SE,S,SW,W,NW', card: 'N,E,S,W',
	},
	zh: {
		title: '指南针', calibrate: '校准', sensors: '传感器', pitch: '俯仰', roll: '横滚',
		accel: '加速度计', gyro: '陀螺仪', mag: '磁力计', calibration: '校准',
		calibrated: '已校准', notCalibrated: '未校准', calTitle: '校准指南针',
		rotate: '将 CardputerZero 缓慢转向各个方向', turn: '翻转设备，让每一面都转到', metal: '远离金属和磁铁',
		save: '无法保存校准', done: '完成', calDone: '指南针已校准', cancel: '取消', noMag: '没有磁力计',
		noImu: '运动传感器不可用', dirs: '北,东北,东,东南,南,西南,西,西北', card: '北,东,南,西',
	},
});

const $ = (id) => document.getElementById(id);
const NS = 'http://www.w3.org/2000/svg';
let state = null;
let shown = null; // unwrapped dial angle being displayed
let target = null;

function el(tag, attrs, parent) {
	const e = document.createElementNS(NS, tag);
	for (const k in attrs) e.setAttribute(k, attrs[k]);
	parent.appendChild(e);
	return e;
}

function buildRose() {
	const g = $('rose');
	g.textContent = '';
	el('circle', { r: 150, class: 'face' }, g);
	for (let d = 0; d < 360; d += 2) {
		const major = d % 30 === 0;
		const r1 = major ? 128 : d % 10 === 0 ? 133 : 137;
		const a = (d * Math.PI) / 180;
		el('line', {
			x1: Math.sin(a) * r1, y1: -Math.cos(a) * r1, x2: Math.sin(a) * 146, y2: -Math.cos(a) * 146,
			class: major ? 'tick major' : 'tick',
		}, g);
	}
	const card = t('card').split(',');
	for (let d = 0; d < 360; d += 30) {
		const a = (d * Math.PI) / 180;
		const isCard = d % 90 === 0;
		const r = isCard ? 100 : 112;
		const txt = el('text', {
			x: Math.sin(a) * r, y: -Math.cos(a) * r, class: isCard ? (d === 0 ? 'cardinal north' : 'cardinal') : 'deg',
			'text-anchor': 'middle', 'dominant-baseline': 'central', transform: `rotate(${d} ${Math.sin(a) * r} ${-Math.cos(a) * r})`,
		}, g);
		txt.textContent = isCard ? card[d / 90] : String(d);
	}
	el('path', { d: 'M0,-150 L-7,-139 L7,-139 Z', class: 'north-mark' }, g);
}

function fmt(v, digits) {
	return (v >= 0 ? ' ' : '') + v.toFixed(digits);
}

function render() {
	const s = state;
	if (!s) return;
	const heading = s.available && s.magnetometer && s.heading_valid;
	if (heading) {
		$('deg').textContent = Math.round(s.heading) % 360 + '°';
		$('dir').textContent = t('dirs').split(',')[Math.round(s.heading / 45) % 8];
		target = -s.heading;
		if (shown === null) shown = target;
	} else {
		$('deg').textContent = '–';
		$('dir').textContent = '';
	}
	$('pitch').textContent = s.available ? Math.round(s.pitch) + '°' : '–';
	$('roll').textContent = s.available ? Math.round(s.roll) + '°' : '–';
	const b = $('bubble');
	b.setAttribute('cx', (s.bubble_x * 26).toFixed(2));
	b.setAttribute('cy', (s.bubble_y * 26).toFixed(2));
	b.classList.toggle('level', s.available && Math.abs(s.pitch) < 1.5 && Math.abs(s.roll) < 1.5);

	const notice = $('notice');
	let text = '';
	let btn = false;
	if (!s.available) text = t('noImu');
	else if (!s.magnetometer) text = t('noMag');
	else if (!s.calibrated && !s.calibration.running) {
		text = t('notCalibrated');
		btn = true;
	}
	notice.hidden = !text;
	$('notice-text').textContent = text;
	$('notice-btn').hidden = !btn;
	$('calibrate').disabled = !s.magnetometer;
	$('cal-state').textContent = s.calibrated ? t('calibrated') : t('notCalibrated');

	const vecs = { accel: [s.accel, 2, 1], gyro: [s.gyro, 1, 180 / Math.PI], mag: [s.mag, 1, 1] };
	for (const card of document.querySelectorAll('[data-vec]')) {
		const [v, digits, mul] = vecs[card.dataset.vec];
		if (!card.firstChild) {
			for (const axis of ['X', 'Y', 'Z']) {
				const row = document.createElement('div');
				row.className = 'zd-row';
				row.innerHTML = `<span>${axis}</span><span class="zd-value"></span>`;
				card.appendChild(row);
			}
		}
		[...card.children].forEach((row, i) => {
			row.lastChild.textContent = s.available ? fmt(v[i] * mul, digits) + ' ' + card.dataset.unit : '–';
		});
	}
	renderSheet();
}

let sheetOpen = false;
function renderSheet() {
	const c = state.calibration;
	if (!sheetOpen) return;
	const p = Math.round(c.progress * 100);
	$('ring').style.strokeDasharray = `${p} 100`;
	$('pct').textContent = c.done ? '' : p + '%';
	$('sheet').classList.toggle('done', c.done);
	$('sheet-title').textContent = c.done ? t('calDone') : t('calTitle');
	$('sheet-text').textContent = c.done ? '' : t(c.hint || 'rotate');
	$('sheet-cancel').textContent = c.done ? t('done') : t('cancel');
	$('sheet-cancel').classList.toggle('primary', c.done);
}

async function post(path) {
	await fetch(path, { method: 'POST' });
}

async function startCalibration() {
	await post('api/calibration/start');
	sheetOpen = true;
	$('sheet').hidden = false;
	state.calibration = { running: true, progress: 0, hint: 'rotate', done: false };
	renderSheet();
}

async function closeSheet() {
	sheetOpen = false;
	$('sheet').hidden = true;
	await post('api/calibration/cancel'); // stops a capture, or clears the "done" flag
}

$('calibrate').onclick = startCalibration;
$('notice-btn').onclick = startCalibration;
$('sheet-cancel').onclick = closeSheet;
document.addEventListener('keydown', (e) => {
	if (sheetOpen && (e.key === 'Escape' || e.key === 'Enter')) closeSheet();
});
$('inspector').onclick = () => {
	const open = $('panel').hidden;
	$('panel').hidden = !open;
	$('inspector').setAttribute('aria-pressed', String(open));
};

// dial: ease towards the target along the shortest way round
function frame() {
	if (target !== null && shown !== null) {
		let d = target - shown;
		d = ((((d + 180) % 360) + 360) % 360) - 180;
		shown += d * 0.25;
		$('rose').setAttribute('transform', `rotate(${shown.toFixed(2)})`);
	}
	requestAnimationFrame(frame);
}

zd.on('lang', () => {
	buildRose();
	render();
});
buildRose();
requestAnimationFrame(frame);

fetch('api/state')
	.then((r) => r.json())
	.then((s) => {
		state = s;
		render();
	});
const es = new EventSource('api/stream');
es.addEventListener('state', (e) => {
	state = JSON.parse(e.data);
	render();
});
