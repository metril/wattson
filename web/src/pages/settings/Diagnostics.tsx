import { useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { CircleCheck, Loader2, OctagonAlert } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';

type Diag = components['schemas']['Diagnostics'];
type DeviceDiag = components['schemas']['DeviceDiag'];
type ProxyDiag = components['schemas']['ProxyDiag'];

const POLL_MS = 10_000;
const SCAN_S = 4;

const fetchDiag = async (scan_s: number) =>
  unwrap(await api.GET('/api/diagnostics/bluetooth', { params: { query: { scan_s } } }));

function Badge({ status }: { status: 'ok' | 'error' }) {
  const ok = status === 'ok';
  const Icon = ok ? CircleCheck : OctagonAlert;
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-control border px-2.5 py-0.5 text-sm font-medium ${ok ? 'border-ok text-ok' : 'border-danger text-danger'}`}
    >
      <Icon aria-hidden className="size-4" />
      {status}
    </span>
  );
}

/** Copies via the async clipboard, or a selection when the page is not a secure context. */
async function copyText(text: string, source: HTMLElement | null): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    if (!source) return false;
    const sel = getSelection();
    const range = document.createRange();
    range.selectNodeContents(source);
    sel?.removeAllRanges();
    sel?.addRange(range);
    try {
      return document.execCommand('copy');
    } catch {
      return false;
    }
  }
}

/** The fix text exactly as the server sent it, with a Copy button. */
function Fix({ text }: { text: string }) {
  const pre = useRef<HTMLPreElement>(null);
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  return (
    <div className="flex flex-col gap-2">
      <pre
        ref={pre}
        className="overflow-x-auto whitespace-pre-wrap break-words rounded-control border border-line bg-raised p-3 font-mono text-sm"
      >
        {text}
      </pre>
      <div className="flex items-center gap-3">
        <Button
          onClick={async () => setState((await copyText(text, pre.current)) ? 'copied' : 'failed')}
        >
          Copy
        </Button>
        {state === 'copied' && <span className="text-sm text-ok">Copied</span>}
        {state === 'failed' && <span className="text-sm text-danger">Copy failed: select the text above.</span>}
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  const id = `diag-${title.replace(/\W+/g, '-').toLowerCase()}`;
  return (
    <Card className="flex flex-col gap-4" role="region" aria-labelledby={id}>
      <h2 id={id} className="numeral text-xl">
        {title}
      </h2>
      {children}
    </Card>
  );
}

function LocalCard({ d }: { d: NonNullable<Diag['local']> }) {
  return (
    <Section title="Local adapter">
      <div className="flex flex-wrap items-center gap-3">
        <Badge status={d.status} />
        <p className="min-w-0 break-words">{d.message}</p>
      </div>
      {d.code && <p className="text-xs text-dim">Code: {d.code}</p>}
      {d.fix && <Fix text={d.fix} />}
    </Section>
  );
}

const chip = 'rounded-control border px-2 py-0.5 font-mono text-xs';

function ProxyCard({ d }: { d: ProxyDiag }) {
  const held = d.subscription === 'held_by_other';
  const slots = d.limit > 0 ? `Slots: ${d.free} of ${d.limit} free` : 'Slots: not reported';
  return (
    <Section title="ESPHome proxy">
      <div className="flex flex-wrap items-center gap-3">
        <Badge status={d.status} />
        <p className="min-w-0 break-words">{d.message}</p>
      </div>
      {d.code && <p className="text-xs text-dim">Code: {d.code}</p>}
      {!held && d.fix && <Fix text={d.fix} />}
      {(d.name || d.version || d.mac) && (
        <p className="text-sm text-dim">
          {[d.name, d.version && `ESPHome ${d.version}`, d.mac].filter(Boolean).join(' · ')}
        </p>
      )}
      <ul aria-label="Proxy features" className="flex flex-wrap gap-2">
        {d.flags.map((f) => (
          <li key={f} className={`${chip} border-line text-dim`}>
            {f}
          </li>
        ))}
        {d.missing_flags.map((f) => (
          <li key={f} className={`${chip} border-danger text-danger`}>
            {f}
          </li>
        ))}
      </ul>
      {d.missing_flags.length > 0 && (
        <p className="text-xs text-danger">Missing from the firmware: {d.missing_flags.join(', ')}</p>
      )}
      <div className="flex flex-col gap-1">
        <p>{slots}</p>
        {d.allocated.length > 0 && (
          <ul className="flex flex-wrap gap-x-4 font-mono text-sm text-dim">
            {d.allocated.map((a) => (
              <li key={a}>{a}</li>
            ))}
          </ul>
        )}
      </div>
      <div className="flex flex-col gap-2">
        <p>
          Subscription:{' '}
          {d.subscription === 'ok' ? (
            <span className="text-ok">OK</span>
          ) : held ? (
            <span className="font-medium text-danger">Held by another client</span>
          ) : (
            <span className="text-dim">Unknown (not connected yet)</span>
          )}
        </p>
        {held && d.fix && <Fix text={d.fix} />}
      </div>
    </Section>
  );
}

function DevicesTable({ devices, rssi }: { devices: DeviceDiag[]; rssi: (d: DeviceDiag) => number | undefined }) {
  return (
    <Card className="flex flex-col gap-3">
      <h2 className="numeral text-xl">Devices</h2>
      <div className="overflow-x-auto rounded-control border border-line">
        <table className="w-full text-left text-sm">
          <thead className="bg-raised text-dim">
            <tr>
              <th className="px-3 py-2 font-medium">Role</th>
              <th className="px-3 py-2 font-medium">Address</th>
              <th className="px-3 py-2 font-medium">State</th>
              <th className="px-3 py-2 font-medium">Signal</th>
              <th className="px-3 py-2 font-medium">Hint</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => {
              const r = rssi(d);
              return (
                <tr key={`${d.role}-${d.address}`} className="border-t border-line align-top">
                  <td className="px-3 py-2">{d.role === 'hr' ? 'Heart rate' : 'Bike'}</td>
                  <td className="px-3 py-2 font-mono">{d.address || '—'}</td>
                  <td className="px-3 py-2">{d.state}</td>
                  <td className="px-3 py-2 whitespace-nowrap">{r == null ? 'not seen' : `${r} dBm`}</td>
                  <td className="px-3 py-2">
                    {d.error && <p className="text-danger">{d.error}</p>}
                    {d.hint ?? (d.error ? '' : '—')}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

/** Bluetooth health: the local adapter or the ESPHome proxy, and the configured devices. */
export function DiagnosticsPage() {
  // Signal strength comes only from an explicit scan; polls never scan.
  const [scan, setScan] = useState<Diag | null>(null);
  const [scanning, setScanning] = useState(false);
  const q = useQuery({
    queryKey: ['diagnostics', 'bluetooth'],
    queryFn: () => fetchDiag(0),
    // No polling while a scan runs: the two would fight for the proxy.
    refetchInterval: scanning ? false : POLL_MS,
    retry: false,
  });
  const [scanError, setScanError] = useState<string | null>(null);

  async function runScan() {
    setScanning(true);
    setScanError(null);
    try {
      setScan(await fetchDiag(SCAN_S));
    } catch (e) {
      setScanError(errMessage(e));
    } finally {
      setScanning(false);
    }
  }

  const rssi = (d: DeviceDiag) =>
    scan ? scan.devices.find((x) => x.role === d.role && x.address === d.address)?.rssi : d.rssi;

  const data = q.data;
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button onClick={runScan} disabled={scanning || q.isPending}>
          {scanning && <Loader2 aria-hidden className="size-5 animate-spin" />}
          {scanning ? `Scanning ${SCAN_S} s…` : 'Scan for signal strength'}
        </Button>
        {q.data?.transport === 'esphome' && (
          <span className="text-sm text-dim">
            A scan briefly takes the proxy's Bluetooth from Home Assistant.
          </span>
        )}
        {scan && !scanning && (
          <span className="text-sm text-dim">Signal measured {new Date(scan.checked_at).toLocaleTimeString()}</span>
        )}
      </div>
      {scanError && <Banner tone="error">{scanError}</Banner>}
      {q.isPending && (
        <p role="status" className="flex items-center gap-2 text-dim">
          <Loader2 aria-hidden className="size-5 animate-spin" />
          Checking Bluetooth. This can take up to 20 seconds.
        </p>
      )}
      {q.isError && <Banner tone="error">{errMessage(q.error)}</Banner>}
      {data?.transport === 'local' && data.local && <LocalCard d={data.local} />}
      {data?.transport === 'esphome' && data.proxy && <ProxyCard d={data.proxy} />}
      {data?.transport === 'none' && (
        <Card>
          <p>No Bluetooth in use (simulator or Home Assistant driver)</p>
        </Card>
      )}
      {data && data.devices.length > 0 && <DevicesTable devices={data.devices} rssi={rssi} />}
    </div>
  );
}
