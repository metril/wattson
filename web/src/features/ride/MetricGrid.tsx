import { useState, type ReactNode } from 'react';
import { distUnit, fixed0, hms, speedUnit, toDist, toSpeed, type Units } from '@/lib/format';
import { useTickText, type TickStore } from '@/lib/tick-store';
import { COMPACT_QUERY, useMedia } from '@/lib/use-media';
import type { BikeStatus, Tick } from '@/lib/ws-types';

/** Value with its unit beside it at 0.45x (see .metric-unit). Written straight to the DOM by useTickText. */
function Value({ ticks, pick, unit, className = '' }: { ticks: TickStore; pick: (t: Tick) => string; unit?: string; className?: string }) {
  const ref = useTickText(ticks, pick);
  return (
    <div className={`metric-value ${className}`}>
      <span ref={ref} className="numeral" />
      {unit && <span className="metric-unit">{unit}</span>}
    </div>
  );
}

function Tile({
  name,
  unit,
  ticks,
  pick,
  cls = '',
  valueCls = '',
  children,
}: {
  name: string;
  unit?: string;
  ticks: TickStore;
  pick: (t: Tick) => string;
  cls?: string;
  valueCls?: string;
  children?: ReactNode;
}) {
  return (
    <div className={`metric-tile ${cls}`}>
      <p className="metric-label">
        <span>{name}</span>
      </p>
      <Value ticks={ticks} pick={pick} unit={unit} className={valueCls} />
      {children}
    </div>
  );
}

const pickPower = (t: Tick) => fixed0(t.power_w);
const pickCadence = (t: Tick) => fixed0(t.cadence_rpm);
const pickSpeed = (u: Units) => (t: Tick) => toSpeed(t.speed_kmh, u).toFixed(1);
const pickDistance = (u: Units) => (t: Tick) => toDist(t.distance_m, u).toFixed(2);
const pickTime = (t: Tick) => hms(t.elapsed_s);
const pickGrade = (t: Tick) => t.grade_pct.toFixed(1);
const pickAhead = (t: Tick) => t.grade_ahead_pct.toFixed(1);
const pickRemaining = (u: Units) => (t: Tick) => toDist(Math.max(0, t.route_length_m - t.distance_m), u).toFixed(2);
const pickHR = (t: Tick) => (t.heart_rate_bpm > 0 ? fixed0(t.heart_rate_bpm) : '—');
const pickTarget = (t: Tick) => fixed0(t.target_power_w);
const pickLevel = (t: Tick) => `L${t.level} (${t.reported_level > 0 ? t.reported_level : '–'})`;

const HR_SOURCE: Record<string, string> = { ble: 'Bluetooth', ha: 'Home Assistant' };

/** One line on the state of the heart rate source (from the bike status). */
function hrStatus(hr: NonNullable<BikeStatus['hr']>): string {
  const name = HR_SOURCE[hr.source] ?? hr.source;
  return `${name}: ${hr.connected ? 'connected' : (hr.error ?? 'searching')}`;
}

/**
 * The live readouts. Values are written straight to the DOM by useTickText,
 * so this component renders once and never again as ticks arrive (toggling
 * "More metrics" on a phone is the only re-render).
 * `children` fill the level cells (the level controls).
 * On a phone only Power, Speed, Grade (Cadence off-route) and Level (Target in
 * ERG) are shown until "More metrics" is pressed.
 */
export function MetricGrid({
  ticks,
  showLevel = true,
  route = false,
  erg = false,
  hr,
  units = 'metric',
  children,
}: {
  ticks: TickStore;
  showLevel?: boolean;
  /** Route ride: adds grade now, grade ahead and the distance left. */
  route?: boolean;
  /** ERG ride: adds the target power. */
  erg?: boolean;
  /** Heart rate source status; absent when no source is set. */
  hr?: BikeStatus['hr'];
  /** Display units; metric by default. */
  units?: Units;
  children?: ReactNode;
}) {
  const compact = useMedia(COMPACT_QUERY);
  const [more, setMore] = useState(false);
  const rest = !compact || more;

  return (
    <div role="group" aria-label="Ride metrics" className="metric-grid">
      <Tile name="Power" unit="W" ticks={ticks} pick={pickPower} cls="m-power" valueCls="metric-hero" />
      <Tile name="Speed" unit={speedUnit(units)} ticks={ticks} pick={pickSpeed(units)} cls="m-speed" />
      {route ? (
        <Tile name="Grade" unit="%" ticks={ticks} pick={pickGrade} cls="m-grade" />
      ) : (
        <Tile name="Cadence" unit="rpm" ticks={ticks} pick={pickCadence} cls="m-grade" />
      )}
      {erg && <Tile name="Target" unit="W" ticks={ticks} pick={pickTarget} cls="m-level" />}
      {showLevel && !erg && (
        <Tile name="Level" ticks={ticks} pick={pickLevel} cls="m-level" valueCls="ride-level-up" />
      )}
      {children}
      {compact && (
        <button
          type="button"
          data-nav
          aria-expanded={more}
          onClick={() => setMore((v) => !v)}
          className="m-more inline-flex min-h-touch items-center justify-center rounded-control bg-raised px-4 font-medium text-chalk"
        >
          More metrics
        </button>
      )}
      {rest && (
        <>
          {route && <Tile name="Cadence" unit="rpm" ticks={ticks} pick={pickCadence} cls="m-rest" />}
          <Tile name="Distance" unit={distUnit(units)} ticks={ticks} pick={pickDistance(units)} cls="m-rest m-dist" />
          <Tile name="Time" ticks={ticks} pick={pickTime} cls="m-time" />
          <Tile name="HR" unit="bpm" ticks={ticks} pick={pickHR} cls="m-hr">
            {hr && <p className="truncate text-xs text-dim">{hrStatus(hr)}</p>}
          </Tile>
          {erg && showLevel && <Tile name="Level" ticks={ticks} pick={pickLevel} cls="m-rest" />}
          {route && (
            <>
              <Tile name="Ahead" unit="%" ticks={ticks} pick={pickAhead} cls="m-rest" />
              <Tile name="Remaining" unit={distUnit(units)} ticks={ticks} pick={pickRemaining(units)} cls="m-rest" />
            </>
          )}
        </>
      )}
    </div>
  );
}
