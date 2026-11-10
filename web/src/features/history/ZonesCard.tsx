import { Link } from 'react-router';
import type { components } from '@/lib/openapi';
import { HR_ZONES, POWER_ZONES } from '@/lib/zones';
import { Card } from '@/components/Card';
import { InfoPopover } from '@/components/InfoPopover';
import { SectionHeader } from '@/components/SectionHeader';
import { ZoneBars } from './ZoneBars';

function Legend() {
  const block = (title: string, names: readonly { name: string; blurb: string }[]) => (
    <div>
      <h4 className="label mb-1">{title}</h4>
      <ul className="flex flex-col gap-1">
        {names.map((z, i) => (
          <li key={z.name} className="flex gap-2">
            <span className="numeral w-6 shrink-0" style={{ color: `var(--zone-${i + 1})` }}>Z{i + 1}</span>
            <span>
              <span className="font-medium">{z.name}.</span> <span className="text-dim">{z.blurb}</span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
  return (
    <div className="flex flex-col gap-3">
      {block('Power zones (% of FTP)', POWER_ZONES)}
      {block('Heart-rate zones (% of max HR)', HR_ZONES)}
    </div>
  );
}

/** Time in power and heart-rate zones for one ride, with ranges and a legend. */
export function ZonesCard({ ride, maxHr }: { ride: components['schemas']['RideOut']; maxHr?: number | null }) {
  const hasPower = !!ride.power_zone_s && ride.power_zone_s.some((s) => s > 0);
  const hasHr = !!ride.hr_zone_s && ride.hr_zone_s.some((s) => s > 0);
  if (!hasPower && !hasHr && ride.ftp_w) return null;
  return (
    <Card>
      <SectionHeader
        title="Zones"
        hint={hasHr && maxHr ? `Heart-rate ranges are based on current max HR (${maxHr} bpm).` : undefined}
        actions={
          <InfoPopover label="About zones">
            <Legend />
          </InfoPopover>
        }
      />
      {!ride.ftp_w && (
        <p className="mb-3 text-sm text-dim">
          Set FTP to see zones.{' '}
          <Link to="/riders" className="text-accent underline">
            Open Riders
          </Link>
        </p>
      )}
      <div className="grid gap-8 lg:grid-cols-2">
        <ZoneBars title="Time in power zones" kind="power" seconds={ride.power_zone_s} base={ride.ftp_w} />
        <ZoneBars title="Time in heart-rate zones" kind="hr" seconds={ride.hr_zone_s} base={maxHr} />
      </div>
    </Card>
  );
}
