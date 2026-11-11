import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { Badge } from './Badge';
import { Card } from './Card';
import { Segmented } from './Segmented';
import { StatTile } from './StatTile';

describe('StatTile', () => {
  it('shows label, value, unit and delta', () => {
    render(<dl><StatTile label="Avg power" value="212" unit="W" color="power" delta="+4 vs last" /></dl>);
    expect(screen.getByText('Avg power')).toBeInTheDocument();
    expect(screen.getByText('212')).toHaveClass('text-m-power');
    expect(screen.getByText('W')).toBeInTheDocument();
    expect(screen.getByText('+4 vs last')).toBeInTheDocument();
  });
});

describe('Card', () => {
  it('sunken has no border and flat has no shadow', () => {
    render(<><Card variant="sunken">a</Card><Card variant="flat">b</Card></>);
    expect(screen.getByText('a')).not.toHaveClass('border');
    expect(screen.getByText('b')).not.toHaveClass('shadow-panel');
  });
});

describe('Badge', () => {
  it('renders its tone', () => {
    render(<Badge tone="z4">Z4</Badge>);
    expect(screen.getByText('Z4')).toHaveClass('text-zone-4');
  });
});

describe('Segmented', () => {
  function Host() {
    const [v, setV] = useState('a');
    return <Segmented label="Window" value={v} onChange={setV} options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }]} />;
  }
  it('switches choice and never deselects', async () => {
    render(<Host />);
    await userEvent.click(screen.getByRole('radio', { name: 'B' }));
    expect(screen.getByRole('radio', { name: 'B' })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: 'B' }));
    expect(screen.getByRole('radio', { name: 'B' })).toBeChecked();
  });
});
