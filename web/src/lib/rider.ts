const KEY = 'wattson.rider_id';

/** The rider chosen on this device, or null. */
export function getSelectedRiderId(): number | null {
  try {
    const n = Number(localStorage.getItem(KEY));
    return Number.isInteger(n) && n > 0 ? n : null;
  } catch {
    return null;
  }
}

export function setSelectedRiderId(id: number): void {
  try {
    localStorage.setItem(KEY, String(id));
  } catch {
    // storage unavailable; the choice just does not persist
  }
}

export function clearSelectedRiderId(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // storage unavailable
  }
}
