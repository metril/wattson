import { Link } from 'react-router';
import type { components } from '@/lib/openapi';
import { fmtSpeed, hms, rideDate, type Units } from '@/lib/format';

export type Attempt = components['schemas']['AttemptOut'];

/** Attempts on a route, newest first, with the PR marked. Each row opens the ride. */
export function AttemptsTable({ attempts, units = 'metric' }: { attempts: Attempt[]; units?: Units }) {
  if (attempts.length === 0) return <p className="text-dim">No rides on this route yet.</p>;
  const rows = [...attempts].reverse();
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left">
        <thead className="text-sm text-dim">
          <tr>
            <th className="py-1 pr-4 font-medium">Date</th>
            <th className="py-1 pr-4 font-medium">Time</th>
            <th className="py-1 pr-4 font-medium">Speed</th>
            <th className="py-1 pr-4 font-medium">NP</th>
            <th className="py-1 font-medium" />
          </tr>
        </thead>
        <tbody>
          {rows.map((a) => (
            <tr key={a.ride_id} className="border-t border-line">
              <td className="py-2 pr-4">
                <Link to={`/history/${a.ride_id}`} className="text-accent">
                  {rideDate(a.started_at)}
                </Link>
              </td>
              <td className="numeral py-2 pr-4">{a.completed ? hms(a.elapsed_s) : <span className="text-dim">Partial</span>}</td>
              <td className="numeral py-2 pr-4">{fmtSpeed(a.avg_speed_kmh, units)}</td>
              <td className="numeral py-2 pr-4">{a.np_w.toFixed(0)} W</td>
              <td className="py-2">
                {a.is_pr && (
                  <span className="rounded-full bg-accent px-2 py-0.5 text-xs font-medium text-accent-ink">PR</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
