import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { EntityPicker } from '@/components/EntityPicker';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { Select } from '@/components/Select';
import { TestButton } from '@/components/TestButton';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

type Device = components['schemas']['Device'];
type Transport = 'local' | 'esphome';

const SCAN_SECONDS = 8;

/** Scan results; a row (or its address) picks the device. */
function DeviceTable({ devices, onPick }: { devices: Device[]; onPick: (d: Device) => void }) {
  return (
      <div className="relative overflow-x-auto rounded-control border border-line">
        <table className="w-full text-left text-sm">
          <thead className="bg-raised text-dim">
            <tr>
              <th className="px-3 py-2 font-medium">Address</th>
              <th className="px-3 py-2 font-medium">Name</th>
              <th className="px-3 py-2 font-medium">Signal</th>
              <th className="px-3 py-2 font-medium">Protocol</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => (
              <tr
                key={d.address}
                onClick={() => onPick(d)}
                className="cursor-pointer border-t border-line hover:bg-raised"
              >
                <td className="px-1">
                  <button
                    type="button"
                    className="min-h-touch px-2 text-left font-mono text-accent"
                    onClick={() => onPick(d)}
                  >
                    {d.address}
                  </button>
                </td>
                <td className="px-3 py-2">{d.name || '—'}</td>
                <td className="px-3 py-2 whitespace-nowrap">{d.rssi} dBm</td>
                <td className="px-3 py-2">{d.protocol || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
  );
}

function Body({ s, compact, onSaved }: { s: SettingsOut; compact?: boolean; onSaved?: () => void }) {
  const b = s.bike;
  const f = useSettingsForm(
    {
      'bike.driver': b.driver,
      'bike.transport': b.transport,
      'bike.address': b.address,
      'bike.esphome_host': b.esphome_host,
      'bike.ha_resistance_entity': b.ha_resistance_entity,
      'bike.ha_power_entity': b.ha_power_entity,
      'bike.ha_cadence_entity': b.ha_cadence_entity,
      'bike.idle_release_min': b.idle_release_min,
      'bike.sim_cadence_rpm': b.sim_cadence_rpm,
    },
    onSaved,
  );
  const driver = f.value<string>('bike.driver');
  const transport = f.value<Transport>('bike.transport');
  const host = f.value<string>('bike.esphome_host');
  const key = f.value<string | undefined>('bike.esphome_key');
  const address = f.value<string>('bike.address');
  const sim = driver === 'sim';
  const ha = driver === 'ha';
  const radio = !sim && !ha;
  const esphome = radio && transport === 'esphome';

  const [scanning, setScanning] = useState(false);
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [scanError, setScanError] = useState<string | null>(null);

  // Typed values only; empty falls back to the saved settings on the server.
  const proxy = esphome ? { ...(host ? { host } : {}), ...(key ? { key } : {}) } : {};

  async function scan() {
    setScanning(true);
    setScanError(null);
    try {
      const r = unwrap(await api.POST('/api/bike/scan', { body: { duration_s: SCAN_SECONDS, transport, ...proxy } }));
      setDevices(r.devices);
    } catch (e) {
      setDevices(null);
      setScanError(errMessage(e));
    } finally {
      setScanning(false);
    }
  }

  const num = (k: string, label: string, min: number, max: number, hint: string) => {
    const v = f.value<number>(k);
    return (
      <Field
        label={label}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        value={Number.isNaN(v) ? '' : v}
        onChange={(e) => f.edit(k, e.target.value === '' ? NaN : Number(e.target.value))}
        error={f.fieldError(k)}
        hint={hint}
      />
    );
  };

  const shown = [
    'bike.driver',
    'bike.idle_release_min',
    ...(sim ? ['bike.sim_cadence_rpm'] : []),
    ...(ha ? ['bike.ha_resistance_entity', 'bike.ha_power_entity', 'bike.ha_cadence_entity'] : []),
    ...(radio ? ['bike.transport', 'bike.address'] : []),
    ...(esphome ? ['bike.esphome_host', 'bike.esphome_key'] : []),
  ];

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit(shown);
      }}
    >
      <FormCard title="Bike connection" compact={compact}>
        <Select
          label="Driver"
          value={driver}
          onChange={(e) => f.edit('bike.driver', e.target.value)}
          hint="Simulator needs no hardware. Auto-detect picks FitPlus or FTMS from what the bike advertises."
        >
          <option value="sim">Simulator</option>
          <option value="auto">Auto-detect</option>
          <option value="fitplus">FitPlus</option>
          <option value="ftms">FTMS</option>
          <option value="ha">Home Assistant (FTMS)</option>
        </Select>

        {ha && (
          <>
            <Banner tone="info">
              Home Assistant can only drive FTMS bikes through its FTMS integration. If Wattson detected FitPlus, use
              the local adapter or an ESPHome proxy.
            </Banner>
            <EntityPicker
              domain="number"
              label="Resistance entity"
              value={f.value<string>('bike.ha_resistance_entity')}
              onChange={(id) => f.edit('bike.ha_resistance_entity', id)}
              hint="The number entity that sets the resistance level."
            />
            <EntityPicker
              domain="sensor"
              deviceClass="power"
              label="Power entity"
              value={f.value<string>('bike.ha_power_entity')}
              onChange={(id) => f.edit('bike.ha_power_entity', id)}
              hint="A sensor reporting watts."
            />
            <EntityPicker
              domain="sensor"
              label="Cadence entity"
              value={f.value<string>('bike.ha_cadence_entity')}
              onChange={(id) => f.edit('bike.ha_cadence_entity', id)}
              hint="A sensor reporting revolutions per minute."
            />
            {['bike.ha_resistance_entity', 'bike.ha_power_entity', 'bike.ha_cadence_entity'].map((k) => {
              const e = f.fieldError(k);
              return e ? (
                <p key={k} className="text-xs text-danger">
                  {e}
                </p>
              ) : null;
            })}
          </>
        )}

        {radio && (
          <>
            <div role="radiogroup" aria-label="Transport" className="flex flex-col gap-2">
              <span className="text-sm font-medium text-dim">Transport</span>
              <div className="flex flex-wrap gap-2">
                {(
                  [
                    ['local', 'Local adapter'],
                    ['esphome', 'ESPHome proxy'],
                  ] as const
                ).map(([v, label]) => (
                  <label
                    key={v}
                    className="flex min-h-touch flex-1 cursor-pointer items-center gap-3 rounded-control border border-line bg-raised px-4 has-[:checked]:border-accent"
                  >
                    <input
                      type="radio"
                      name="bike-transport"
                      value={v}
                      checked={transport === v}
                      onChange={() => f.edit('bike.transport', v)}
                      className="size-5 accent-accent"
                    />
                    {label}
                  </label>
                ))}
              </div>
              {f.fieldError('bike.transport') && (
                <p className="text-xs text-danger">{f.fieldError('bike.transport')}</p>
              )}
            </div>

            {esphome && (
              <>
                <Field
                  label="ESPHome host"
                  placeholder="bt-proxy.local or 192.168.1.50:6053"
                  autoComplete="off"
                  value={host}
                  onChange={(e) => f.edit('bike.esphome_host', e.target.value)}
                  error={f.fieldError('bike.esphome_host')}
                  hint="Host or host:port of the Bluetooth proxy (default port 6053)."
                />
                <SecretInput
                  key={`key-${f.version}`}
                  label="ESPHome API key"
                  isSet={b.esphome_key_set}
                  onChange={f.secret('bike.esphome_key')}
                  error={f.fieldError('bike.esphome_key')}
                  hint="Base64 API encryption key from the proxy's ESPHome config."
                />
                <TestButton
                  label="Test ESPHome"
                  run={async () => unwrap(await api.POST('/api/settings/test/esphome', { body: proxy }))}
                />
              </>
            )}

            <div className="flex flex-col gap-3">
              <Field
                label="Bike address"
                placeholder="AA:BB:CC:DD:EE:FF"
                autoComplete="off"
                autoCapitalize="characters"
                value={address}
                onChange={(e) => f.edit('bike.address', e.target.value)}
                error={f.fieldError('bike.address')}
                trailing={
                  <Button onClick={scan} disabled={scanning}>
                    {scanning && <Loader2 aria-hidden className="size-5 animate-spin" />}
                    {scanning ? 'Scanning…' : 'Scan'}
                  </Button>
                }
                hint={`Scan listens for ${SCAN_SECONDS} seconds. Pick a row to use its address.`}
              />
              {scanError && <Banner tone="error">{scanError}</Banner>}
              {devices &&
                (devices.length === 0 ? (
                  <p className="text-sm text-dim">No devices found. Wake the bike by pedalling, then scan again.</p>
                ) : (
                  <DeviceTable devices={devices} onPick={(d) => f.edit('bike.address', d.address)} />
                ))}
              <TestButton
                label="Test BLE"
                run={async () =>
                  unwrap(
                    await api.POST('/api/settings/test/ble', {
                      body: { transport, ...proxy, ...(address ? { address } : {}) },
                    }),
                  )
                }
              />
            </div>
          </>
        )}

        {sim &&
          num('bike.sim_cadence_rpm', 'Simulator cadence (rpm)', 0, 150, 'Cadence of the simulated bike, 0 to 150.')}
        {num(
          'bike.idle_release_min',
          'Idle auto-release (minutes)',
          0,
          240,
          '0 never releases. A connecting or ready bike with no commands for this long is released; a ride in progress never is.',
        )}
        <SaveBar dirty={f.dirtyFor(shown)} pending={f.pending} saved={f.saved} banner={f.banner(shown)} />
      </FormCard>
    </form>
  );
}

const SOURCES = [
  ['off', 'Off'],
  ['ble', 'Bluetooth'],
  ['ha', 'Home Assistant'],
] as const;

function HeartRateBody({ s }: { s: SettingsOut }) {
  const h = s.heart_rate;
  const f = useSettingsForm({
    'heart_rate.source': h.source,
    'heart_rate.address': h.address,
    'heart_rate.name': h.name,
    'heart_rate.ha_entity': h.ha_entity,
  });
  const source = f.value<string>('heart_rate.source');
  const [scanning, setScanning] = useState(false);
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [scanError, setScanError] = useState<string | null>(null);

  async function scan() {
    setScanning(true);
    setScanError(null);
    try {
      const r = unwrap(await api.POST('/api/bike/scan', { body: { duration_s: SCAN_SECONDS } }));
      setDevices(r.devices.filter((d) => d.protocol === 'hr'));
    } catch (e) {
      setDevices(null);
      setScanError(errMessage(e));
    } finally {
      setScanning(false);
    }
  }

  const shown = [
    'heart_rate.source',
    ...(source === 'ble' ? ['heart_rate.address', 'heart_rate.name'] : []),
    ...(source === 'ha' ? ['heart_rate.ha_entity'] : []),
  ];

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit(shown);
      }}
    >
      <FormCard title="Heart rate">
        <div role="radiogroup" aria-label="Heart rate source" className="flex flex-col gap-2">
          <span className="text-sm font-medium text-dim">Source</span>
          <div className="flex flex-wrap gap-2">
            {SOURCES.map(([v, label]) => (
              <label
                key={v}
                className="flex min-h-touch flex-1 cursor-pointer items-center gap-3 rounded-control border border-line bg-raised px-4 has-[:checked]:border-accent"
              >
                <input
                  type="radio"
                  name="hr-source"
                  value={v}
                  checked={source === v}
                  onChange={() => f.edit('heart_rate.source', v)}
                  className="size-5 accent-accent"
                />
                {label}
              </label>
            ))}
          </div>
          {f.fieldError('heart_rate.source') && (
            <p className="text-xs text-danger">{f.fieldError('heart_rate.source')}</p>
          )}
        </div>

        {source === 'ble' && (
          <>
            <Field
              label="Monitor address"
              placeholder="AA:BB:CC:DD:EE:FF"
              autoComplete="off"
              autoCapitalize="characters"
              value={f.value<string>('heart_rate.address')}
              onChange={(e) => f.edit('heart_rate.address', e.target.value)}
              error={f.fieldError('heart_rate.address')}
              trailing={
                <Button onClick={scan} disabled={scanning}>
                  {scanning && <Loader2 aria-hidden className="size-5 animate-spin" />}
                  {scanning ? 'Scanning…' : 'Scan'}
                </Button>
              }
              hint={`Scan listens for ${SCAN_SECONDS} seconds and lists heart rate monitors only. Pick a row to use it.`}
            />
            <Field
              label="Monitor name"
              autoComplete="off"
              value={f.value<string>('heart_rate.name')}
              onChange={(e) => f.edit('heart_rate.name', e.target.value)}
              error={f.fieldError('heart_rate.name')}
              hint="Phones change their Bluetooth address from time to time; Wattson finds the monitor again by this name. Apple Watch: run HeartCast on an iPhone near the bike's Bluetooth location."
            />
            {scanError && <Banner tone="error">{scanError}</Banner>}
            {devices &&
              (devices.length === 0 ? (
                <p className="text-sm text-dim">
                  No heart rate monitors found. Put on the strap or start HeartCast, then scan again.
                </p>
              ) : (
                <DeviceTable
                  devices={devices}
                  onPick={(d) => {
                    f.edit('heart_rate.address', d.address);
                    f.edit('heart_rate.name', d.name);
                  }}
                />
              ))}
          </>
        )}

        {source === 'ha' && (
          <>
            <EntityPicker
              domain="sensor"
              deviceClass="heart_rate"
              label="Heart rate entity"
              value={f.value<string>('heart_rate.ha_entity')}
              onChange={(id) => f.edit('heart_rate.ha_entity', id)}
              hint="A sensor reporting beats per minute. Needs the Home Assistant URL and token on the Integrations tab."
            />
            {f.fieldError('heart_rate.ha_entity') && (
              <p className="text-xs text-danger">{f.fieldError('heart_rate.ha_entity')}</p>
            )}
          </>
        )}
        <p className="text-xs text-dim">Applies the next time the bike connects.</p>
        <SaveBar dirty={f.dirtyFor(shown)} pending={f.pending} saved={f.saved} banner={f.banner(shown)} />
      </FormCard>
    </form>
  );
}

/** Heart rate source: off, a Bluetooth monitor, or a Home Assistant sensor. */
export function HeartRateForm() {
  return <SettingsGate>{(s) => <HeartRateBody s={s} />}</SettingsGate>;
}

const POWER_MODEL_KEY = ['bike', 'power-model'] as const;
const CONFIRM_MS = 4000;

/**
 * What ERG has learned about this bike: the number of ride samples and the
 * modelled watts at 80 rpm for a few levels. Reset (two taps) discards it.
 */
export function PowerModelCard() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: POWER_MODEL_KEY,
    queryFn: async () => unwrap(await api.GET('/api/bike/power-model')),
  });
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), CONFIRM_MS);
    return () => clearTimeout(t);
  }, [armed]);
  const reset = useMutation({
    mutationFn: async () => unwrap(await api.DELETE('/api/bike/power-model')),
    onSuccess: () => qc.invalidateQueries({ queryKey: POWER_MODEL_KEY }),
  });

  const m = q.data;
  const at = (level: number) => (m && m.watts_at_80[level - 1] !== undefined ? `${Math.round(m.watts_at_80[level - 1])} W` : '–');
  return (
    <FormCard title="Learned power model">
      {q.isError && <Banner tone="error">{errMessage(q.error)}</Banner>}
      {reset.isError && <Banner tone="error">{errMessage(reset.error)}</Banner>}
      {m && (
        <>
          <p className="text-sm text-dim">
            ERG uses this to pick the resistance level for a target power. It learns from your rides on this bike.
            {m.seeded && ' It is still the built-in starting model.'}
          </p>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
            {(
              [
                ['Samples', String(m.samples), 'pm-samples'],
                ['L1 at 80 rpm', at(1), 'pm-l1'],
                ['L8 at 80 rpm', at(8), 'pm-l8'],
                ['L16 at 80 rpm', at(16), 'pm-l16'],
              ] as const
            ).map(([k, v, id]) => (
              <div key={k} className="flex flex-col">
                <dt className="text-sm font-medium text-dim">{k}</dt>
                <dd data-testid={id} className="numeral text-lg">
                  {v}
                </dd>
              </div>
            ))}
          </dl>
          <div>
            <Button
              variant={armed ? 'danger' : 'secondary'}
              disabled={reset.isPending}
              onClick={() => {
                if (!armed) {
                  setArmed(true);
                  return;
                }
                setArmed(false);
                reset.mutate();
              }}
            >
              {armed ? 'Tap again to reset' : 'Reset'}
            </Button>
          </div>
        </>
      )}
    </FormCard>
  );
}

/** The Bike settings tab: the bike, the learned power model, then the heart rate monitor. */
export function BikePage() {
  return (
    <div className="flex flex-col gap-4">
      <BikeForm />
      <PowerModelCard />
      <HeartRateForm />
    </div>
  );
}

export function BikeForm({ compact, onSaved }: { compact?: boolean; onSaved?: () => void } = {}) {
  return <SettingsGate>{(s) => <Body s={s} compact={compact} onSaved={onSaved} />}</SettingsGate>;
}
