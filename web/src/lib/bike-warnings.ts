/**
 * Whether Status.last_error says the bike could not be confirmed back at its
 * minimum level. The API has no structured field for this (only the 424
 * floor_unconfirmed answer to release), so this matches the wording of the
 * manager's message. Replace with a status flag if the server grows one.
 */
export function isFloorUnconfirmed(lastError: string | undefined): boolean {
  return !!lastError && /minimum level|confirm/i.test(lastError);
}
