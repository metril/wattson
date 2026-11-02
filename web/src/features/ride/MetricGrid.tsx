import type { ReactNode } from 'react';
import { fixed0, hms, km } from '@/lib/format';
import { useTickText, type TickStore } from '@/lib/tick-store';
import type { BikeStatus, Tick } from '@/lib/ws-types';

function Value({ ticks, pick, className }: { ticks: TickStore; pick: (t: Tick) => string; className: string }) {
  const ref = useTickText(ticks, pick);
  return <span ref={ref} className={`numeral block ${className}`} />;
}

const tile = 'flex min-w-0 flex-col justify-between gap-1 rounded-panel border border-line bg-panel px-4 py-3';
const label = 'flex items-baseline gap-1.5 text-sm font-medium text-dim';
// On a 1080p TV every secondary readout uses the same large size.
const tv = 'min-[1600px]:text-[clamp(3rem,6vw,7rem)]';

const pickPower = (t: Tick) => fixed0(t.power_w);
const pickCadence = (t: Tick) => fixed0(t.cadence_rpm);
const pickSpeed = (t: Tick) => t.speed_kmh.toFixed(1);
const pickDistance = (t: Tick) => km(t.distance_m);
const pickTime = (t: Tick) => hms(t.elapsed_s);
const pickGrade = (t: Tick) => t.grade_pct.toFixed(1);
const pickAhead = (t: Tick) => t.grade_ahead_pct.toFixed(1);
const pickRemaining = (t: Tick) => km(Math.max(0, t.route_length_m - t.distance_m));
const pickHR = (t: Tick) => (t.heart_rate_bpm > 0 ? fixed0(t.heart_rate_bpm) : '—');
const pickLevel = (t: Tick) => `L${t.level} (${t.reported_level > 0 ? t.reported_level : '–'})`;

const HR_SOURCE: Record<string, string> = { ble: 'Bluetooth', ha: 'Home Assistant' };

/** One line on the state of the heart rate source (from the bike status). */
function hrStatus(hr: NonNullable<BikeStatus['hr']>): string {
  const name = HR_SOURCE[hr.source] ?? hr.source;
  return `${name}: ${hr.connected ? 'connected' : (hr.error ?? 'searching')}`;
}

/**
 * The live readouts. Values are written straight to the DOM by useTickText,
 * so this component renders once and never again as ticks arrive.
 * `children` fill the last cells (the level controls).
 */
export function MetricGrid({
  ticks,
  showLevel = true,
  route = false,
  hr,
  children,
}: {
  ticks: TickStore;
  showLevel?: boolean;
  /** Route ride: adds grade now, grade ahead and the distance left. */
  route?: boolean;
  /** Heart rate source status; absent when no source is set. */
  hr?: BikeStatus['hr'];
  children?: ReactNode;
}) {
  return (
    <div role="group" aria-label="Ride metrics" className="grid grid-cols-2 gap-3 md:grid-cols-3 md:gap-4">
      <div className={`${tile} col-span-2 md:col-span-3`}>
        <p className={label}>
          <span>Power</span>
          <span>W</span>
        </p>
        <Value ticks={ticks} pick={pickPower} className="text-numeral-xl" />
      </div>
      <div className={tile}>
        <p className={label}>
          <span>Cadence</span>
          <span>rpm</span>
        </p>
        <Value ticks={ticks} pick={pickCadence} className={`text-numeral-lg ${tv}`} />
      </div>
      <div className={tile}>
        <p className={label}>
          <span>Speed</span>
          <span>km/h</span>
        </p>
        <Value ticks={ticks} pick={pickSpeed} className={`text-numeral-lg ${tv}`} />
      </div>
      <div className={tile}>
        <p className={label}>
          <span>Distance</span>
          <span>km</span>
        </p>
        <Value ticks={ticks} pick={pickDistance} className={`text-numeral-md ${tv}`} />
      </div>
      <div className={tile}>
        <p className={label}>
          <span>Time</span>
        </p>
        <Value ticks={ticks} pick={pickTime} className={`text-numeral-md ${tv}`} />
      </div>
      <div className={tile}>
        <p className={label}>
          <span>HR</span>
          <span>bpm</span>
        </p>
        <Value ticks={ticks} pick={pickHR} className={`text-numeral-md ${tv}`} />
        {hr && <p className="truncate text-xs text-dim">{hrStatus(hr)}</p>}
      </div>
      {route && (
        <>
          <div className={tile}>
            <p className={label}>
              <span>Grade</span>
              <span>%</span>
            </p>
            <Value ticks={ticks} pick={pickGrade} className={`text-numeral-md ${tv}`} />
          </div>
          <div className={tile}>
            <p className={label}>
              <span>Ahead</span>
              <span>%</span>
            </p>
            <Value ticks={ticks} pick={pickAhead} className={`text-numeral-md ${tv}`} />
          </div>
          <div className={tile}>
            <p className={label}>
              <span>Remaining</span>
              <span>km</span>
            </p>
            <Value ticks={ticks} pick={pickRemaining} className={`text-numeral-md ${tv}`} />
          </div>
        </>
      )}
      {showLevel && (
        <div className={tile}>
          <p className={label}>
            <span>Level</span>
          </p>
          <Value ticks={ticks} pick={pickLevel} className={`text-numeral-md ${tv}`} />
        </div>
      )}
      {children}
    </div>
  );
}
