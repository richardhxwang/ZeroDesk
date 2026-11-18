// ZeroDesk app SDK (docs/APP-SPEC.md §7). Served at _zd/sdk.js; defines window.zd.
// Talks to the ZeroDesk window that hosts this page over postMessage.
(function () {
	'use strict';
	if (window.zd) return;

	var parent = window.parent;
	var seq = 0;
	var pending = {};
	var listeners = {};
	var state = { app: null, theme: 'light', lang: 'en', permissions: [] };
	var resolveReady;
	var ready = new Promise(function (r) {
		resolveReady = r;
	});

	function call(method, params) {
		return new Promise(function (resolve, reject) {
			var id = ++seq;
			pending[id] = { resolve: resolve, reject: reject };
			parent.postMessage({ zd: 1, id: id, method: method, params: params || {} }, '*');
		});
	}

	function emit(event, data) {
		(listeners[event] || []).slice().forEach(function (fn) {
			try {
				fn(data);
			} catch (e) {
				console.error(e);
			}
		});
	}

	function applyTheme(theme) {
		state.theme = theme === 'dark' ? 'dark' : 'light';
		document.documentElement.dataset.theme = state.theme;
		document.documentElement.style.colorScheme = state.theme;
	}

	window.addEventListener('message', function (e) {
		if (e.source !== parent) return;
		var m = e.data;
		if (!m || m.zd !== 1) return;
		if (m.id && pending[m.id]) {
			var p = pending[m.id];
			delete pending[m.id];
			if (m.error) p.reject(new Error(m.error));
			else p.resolve(m.result);
			return;
		}
		if (m.event === 'init') {
			state.app = m.data.app;
			state.lang = m.data.lang;
			state.permissions = m.data.permissions || [];
			applyTheme(m.data.theme);
			resolveReady({ app: state.app, theme: state.theme, lang: state.lang, permissions: state.permissions.slice() });
		} else if (m.event === 'theme') {
			applyTheme(m.data);
			emit('theme', state.theme);
		} else if (m.event === 'lang') {
			state.lang = m.data;
			emit('lang', state.lang);
		} else if (m.event) {
			emit(m.event, m.data);
		}
	});

	// Window dragging from regions marked data-zd-drag (titlebar "app" mode).
	// The pointer is captured here, so moves are forwarded to the host.
	var drag = null;
	document.addEventListener('pointerdown', function (e) {
		if (e.button !== 0) return;
		var t = e.target;
		if (!t.closest || !t.closest('[data-zd-drag]')) return;
		if (t.closest('button, input, select, textarea, a, label, [data-zd-no-drag]')) return;
		drag = { id: e.pointerId, x: e.screenX, y: e.screenY, el: t };
		try {
			t.setPointerCapture(e.pointerId);
		} catch (_) {}
		parent.postMessage({ zd: 1, method: 'window.dragStart', params: {} }, '*');
	});
	document.addEventListener('pointermove', function (e) {
		if (!drag || e.pointerId !== drag.id) return;
		var dx = e.screenX - drag.x;
		var dy = e.screenY - drag.y;
		if (!dx && !dy) return;
		drag.x = e.screenX;
		drag.y = e.screenY;
		parent.postMessage({ zd: 1, method: 'window.dragMove', params: { dx: dx, dy: dy } }, '*');
	});
	function endDrag(e) {
		if (!drag || e.pointerId !== drag.id) return;
		drag = null;
		parent.postMessage({ zd: 1, method: 'window.dragEnd', params: {} }, '*');
	}
	document.addEventListener('pointerup', endDrag);
	document.addEventListener('pointercancel', endDrag);
	document.addEventListener('dblclick', function (e) {
		var t = e.target;
		if (t.closest && t.closest('[data-zd-drag]') && !t.closest('button, input, select, textarea, a, label')) {
			parent.postMessage({ zd: 1, method: 'window.zoom', params: {} }, '*');
		}
	});
	// focus the window when the page is clicked
	document.addEventListener(
		'pointerdown',
		function () {
			parent.postMessage({ zd: 1, method: 'window.focus', params: {} }, '*');
		},
		true,
	);

	function blobToBase64(blob) {
		return blob.arrayBuffer().then(function (buf) {
			var bytes = new Uint8Array(buf);
			var s = '';
			for (var i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
			return btoa(s);
		});
	}

	// zd.localize({ en: {...}, zh: {...} }): fills [data-i18n] text and
	// [data-i18n-title] tooltips, again on every language change; returns t(key).
	function localize(strings) {
		function t(key) {
			var table = strings[state.lang] || strings.en || {};
			return key in table ? table[key] : (strings.en || {})[key] || key;
		}
		function apply() {
			document.documentElement.lang = state.lang;
			document.querySelectorAll('[data-i18n]').forEach(function (el) {
				el.textContent = t(el.dataset.i18n);
			});
			document.querySelectorAll('[data-i18n-title]').forEach(function (el) {
				el.title = t(el.dataset.i18nTitle);
				el.setAttribute('aria-label', el.title);
			});
			document.querySelectorAll('[data-i18n-placeholder]').forEach(function (el) {
				el.placeholder = t(el.dataset.i18nPlaceholder);
			});
		}
		apply();
		ready.then(apply);
		(listeners.lang = listeners.lang || []).push(apply);
		return t;
	}

	window.zd = {
		version: 1,
		localize: localize,
		ready: ready,
		get app() {
			return state.app;
		},
		get theme() {
			return state.theme;
		},
		get lang() {
			return state.lang;
		},
		get permissions() {
			return state.permissions.slice();
		},
		on: function (event, fn) {
			(listeners[event] = listeners[event] || []).push(fn);
			return function () {
				listeners[event] = (listeners[event] || []).filter(function (f) {
					return f !== fn;
				});
			};
		},
		window: {
			setTitle: function (title) {
				return call('window.setTitle', { title: String(title) });
			},
			resize: function (width, height) {
				return call('window.resize', { width: +width, height: +height });
			},
			close: function () {
				return call('window.close');
			},
		},
		storage: {
			get: function (key) {
				return call('storage.get', { key: String(key) });
			},
			set: function (key, value) {
				return call('storage.set', { key: String(key), value: value === undefined ? null : value });
			},
			remove: function (key) {
				return call('storage.set', { key: String(key), value: null });
			},
		},
		notify: function (opts) {
			opts = opts || {};
			return call('notify', { title: String(opts.title || ''), body: String(opts.body || '') });
		},
		files: {
			list: function (path) {
				return call('files.list', { path: String(path) });
			},
			read: function (path) {
				return call('files.read', { path: String(path) }).then(function (r) {
					var bin = atob(r.data);
					var bytes = new Uint8Array(bin.length);
					for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
					return new Blob([bytes], { type: r.type || 'application/octet-stream' });
				});
			},
			write: function (path, blob) {
				var b = blob instanceof Blob ? blob : new Blob([blob]);
				return blobToBase64(b).then(function (data) {
					return call('files.write', { path: String(path), data: data });
				});
			},
		},
		device: {
			info: function () {
				return call('device.info');
			},
		},
	};

	parent.postMessage({ zd: 1, method: 'hello', params: {} }, '*');
})();
