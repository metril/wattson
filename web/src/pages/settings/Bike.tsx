import { useState } from 'react';
import { Loader2 } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { Select } from '@/components/Select';
import { TestButton } from '@/components/TestButton';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

type Device = components['schemas']['Device'];
type Transport = 'local' | 'esphome';

const SCAN_SECONDS = 8;

function Body({ s, compact, onSaved }: { s: SettingsOut; compact?: boolean; onSaved?: () => void }) {
  const b = s.bike;
  const f = useSettingsForm(
    {
      'bike.driver': b.driver,
      'bike.transport': b.transport,
      'bike.address': b.address,
      'bike.esphome_host': b.esphome_host,
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
  const esphome = !sim && transport === 'esphome';

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
    ...(sim ? ['bike.sim_cadence_rpm'] : ['bike.transport', 'bike.address']),
    ...(esphome ? ['bike.esphome_host', 'bike.esphome_key'] : []),
  ];

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit();
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
        </Select>

        {!sim && (
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
                  <div className="overflow-x-auto rounded-control border border-line">
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
                            onClick={() => f.edit('bike.address', d.address)}
                            className="cursor-pointer border-t border-line hover:bg-raised"
                          >
                            <td className="px-1">
                              <button
                                type="button"
                                className="min-h-touch px-2 text-left font-mono text-accent"
                                onClick={() => f.edit('bike.address', d.address)}
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
        <SaveBar dirty={f.dirty} pending={f.pending} saved={f.saved} banner={f.banner(shown)} />
      </FormCard>
    </form>
  );
}

export function BikeForm({ compact, onSaved }: { compact?: boolean; onSaved?: () => void } = {}) {
  return <SettingsGate>{(s) => <Body s={s} compact={compact} onSaved={onSaved} />}</SettingsGate>;
}
