import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Block, WorkoutType } from '@/lib/workout-math';
import { BlockEditor } from './BlockEditor';

/** Holds the blocks so typing behaves as in the page, and records every change. */
function Harness(p: { type: WorkoutType; blocks: Block[]; errors?: Record<string, string>; log: Block[][] }) {
  const [bs, setBs] = useState(p.blocks);
  return (
    <BlockEditor
      type={p.type}
      blocks={bs}
      errors={p.errors ?? {}}
      onChange={(n) => {
        p.log.push(n);
        setBs(n);
      }}
    />
  );
}

const last = (log: Block[][]) => log[log.length - 1];

describe('BlockEditor', () => {
  it('adds a default block', async () => {
    const log: Block[][] = [];
    render(<Harness type="interval" blocks={[{ duration_s: 30, level: 5 }]} log={log} />);
    await userEvent.click(screen.getByRole('button', { name: 'Add block' }));
    expect(last(log)).toEqual([{ duration_s: 30, level: 5 }, { duration_s: 60, level_offset: 0 }]);
  });

  it('moves a block down and up, and removes one', async () => {
    const log: Block[][] = [];
    render(<Harness type="interval" blocks={[{ duration_s: 30, level: 5 }, { duration_s: 90, level: 9 }]} log={log} />);
    await userEvent.click(screen.getByRole('button', { name: 'Move down block 1' }));
    expect(last(log).map((b) => b.duration_s)).toEqual([90, 30]);
    await userEvent.click(screen.getByRole('button', { name: 'Move up block 2' }));
    expect(last(log).map((b) => b.duration_s)).toEqual([30, 90]);
    await userEvent.click(screen.getByRole('button', { name: 'Remove block 1' }));
    expect(last(log)).toEqual([{ duration_s: 90, level: 9 }]);
  });

  it('disables moves at the ends and Remove on the last block', () => {
    render(<Harness type="interval" blocks={[{ duration_s: 30, level: 5 }]} log={[]} />);
    expect(screen.getByRole('button', { name: 'Move up block 1' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Move down block 1' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove block 1' })).toBeDisabled();
  });

  it('parses a typed duration', async () => {
    const log: Block[][] = [];
    render(<Harness type="interval" blocks={[{ duration_s: 60, level: 5 }]} log={log} />);
    const f = screen.getByLabelText('Block 1 duration');
    await userEvent.clear(f);
    await userEvent.type(f, '2:00');
    expect(last(log)[0].duration_s).toBe(120);
    expect(f).toHaveValue('2:00');
  });

  it('keeps unparsable text with a hint, and restores it on blur', async () => {
    const log: Block[][] = [];
    render(<Harness type="interval" blocks={[{ duration_s: 60, level: 5 }]} log={log} />);
    const f = screen.getByLabelText('Block 1 duration');
    await userEvent.clear(f);
    await userEvent.type(f, 'ab');
    expect(screen.getByText('Use m:ss, for example 1:30.')).toBeInTheDocument();
    await userEvent.tab();
    expect(f).toHaveValue('1:00');
  });

  it('switches an ERG block to % FTP without leaving power_w behind', async () => {
    const log: Block[][] = [];
    render(<Harness type="erg" blocks={[{ duration_s: 60, power_w: 200 }]} log={log} />);
    await userEvent.selectOptions(screen.getByLabelText('Block 1 target'), '% FTP');
    const v = screen.getByLabelText('Block 1 % ftp value');
    await userEvent.clear(v);
    await userEvent.type(v, '80');
    expect(last(log)).toEqual([{ duration_s: 60, ftp_pct: 80 }]);
    expect(last(log)[0]).not.toHaveProperty('power_w');
  });

  it('keeps the chosen kind while the number is cleared', async () => {
    const log: Block[][] = [];
    render(<Harness type="erg" blocks={[{ duration_s: 60, ftp_pct: 80 }]} log={log} />);
    await userEvent.clear(screen.getByLabelText('Block 1 % ftp value'));
    expect(last(log)).toEqual([{ duration_s: 60 }]);
    expect(screen.getByLabelText('Block 1 target')).toHaveValue('ftp_pct');
  });

  it('sets and clears cadence and label', async () => {
    const log: Block[][] = [];
    render(<Harness type="interval" blocks={[{ duration_s: 60, level: 5 }]} log={log} />);
    await userEvent.type(screen.getByLabelText('Block 1 cadence'), '90');
    await userEvent.type(screen.getByLabelText('Block 1 label'), 'Hard');
    expect(last(log)).toEqual([{ duration_s: 60, level: 5, cadence: '90', label: 'Hard' }]);
    await userEvent.clear(screen.getByLabelText('Block 1 label'));
    expect(last(log)[0]).not.toHaveProperty('label');
  });

  it('renders field errors under their inputs', () => {
    render(
      <Harness
        type="interval"
        blocks={[{ duration_s: 2, level: 5 }]}
        errors={{ 'blocks[0].duration_s': 'Duration must be 0:05 to 2:00:00.', blocks: 'Total too long.' }}
        log={[]}
      />,
    );
    expect(screen.getByText('Duration must be 0:05 to 2:00:00.')).toBeInTheDocument();
    expect(screen.getByLabelText('Block 1 duration')).toHaveAccessibleDescription('Duration must be 0:05 to 2:00:00.');
    expect(screen.getByRole('alert')).toHaveTextContent('Total too long.');
  });

  it('shows what an offset or % FTP block resolves to for a rider', () => {
    render(
      <BlockEditor
        type="erg"
        blocks={[{ duration_s: 60, ftp_pct: 75 }]}
        errors={{}}
        rider={{ offset_level: 5, min_level: 1, max_level: 16, ftp_w: 200 }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText('150 W for this rider')).toBeInTheDocument();
  });
});
