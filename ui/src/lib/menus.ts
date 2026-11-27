import { apps_config } from '🍎/configs/apps/apps-config';
import { apps, close_app, minimize_app, open_app, title_of } from '🍎/state/apps.svelte.ts';
import { t } from '🍎/state/i18n.svelte.ts';
import { preferences, toggle_lang, toggle_scheme } from '🍎/state/preferences.svelte.ts';
import { logout } from '🍎/state/session.svelte.ts';

export type MenuItem = {
	label: string;
	action?: () => void;
	disabled?: boolean;
	shortcut?: string;
	break_after?: boolean;
};

export type MenuDef = { id: string; title: string; items: MenuItem[]; emphasis?: boolean; logo?: boolean };

/** Menus are rebuilt reactively, so labels follow the language switch. */
export function build_menus(): MenuDef[] {
	const active = apps.active;
	const app_title = active ? title_of(active) : 'ZeroDesk';

	return [
		{
			id: 'zero',
			title: 'ZeroDesk',
			logo: true,
			items: [
				{ label: t('menu.about'), action: () => open_app('about'), break_after: true },
				{ label: t('menu.settings'), action: () => open_app('settings'), shortcut: '⌘,', break_after: true },
				{
					label: preferences.theme.scheme === 'dark' ? t('menu.light') : t('menu.dark'),
					action: toggle_scheme,
				},
				{ label: t('menu.lang'), action: toggle_lang, break_after: true },
				{ label: t('menu.reload'), action: () => location.reload() },
				{ label: t('menu.logout'), action: logout },
			],
		},
		{
			id: 'app',
			title: app_title,
			emphasis: true,
			items: [
				{
					label: t('menu.close_window'),
					disabled: !active,
					action: () => active && close_app(active),
					shortcut: '⌥W',
				},
			],
		},
		{
			id: 'window',
			title: t('menu.window'),
			items: [
				{ label: { zh: '最小化', en: 'Minimize', ja: 'しまう' }[preferences.lang], disabled: !active, action: () => active && minimize_app(active), break_after: true },
				...(Object.keys(apps_config) as (keyof typeof apps_config)[]).map((id) => ({
					label: t(apps_config[id].title),
					action: () => open_app(id),
				})),
			],
		},
	];
}
