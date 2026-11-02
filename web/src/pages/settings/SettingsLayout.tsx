import { NavLink, Outlet } from 'react-router';

const tab =
  'flex min-h-touch shrink-0 items-center rounded-control px-4 text-sm font-medium text-dim hover:text-chalk aria-[current=page]:bg-raised aria-[current=page]:text-chalk';

const TABS = [
  ['general', 'General'],
  ['bike', 'Bike'],
  ['integrations', 'Integrations'],
  ['maps', 'Map regions'],
  ['diagnostics', 'Diagnostics'],
  ['auth', 'Authentication'],
  ['users', 'Users'],
] as const;

/** Settings shell: a scrollable tab strip over the active page. */
export function SettingsLayout() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <h1 className="numeral text-2xl">Settings</h1>
      <nav aria-label="Settings" className="-mx-1 flex gap-1 overflow-x-auto px-1">
        {TABS.map(([to, label]) => (
          <NavLink key={to} to={`/settings/${to}`} className={tab}>
            {label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
