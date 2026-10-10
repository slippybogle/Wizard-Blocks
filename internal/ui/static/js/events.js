// Pure functions that turn consecutive engine states into UI events.
// Kept separate (and side-effect free) so they can be tested directly.

/**
 * Detect block events between two states.
 * A block is only celebrated when the engine's blocks_found counter (blocks
 * confirmed on the active chain) increased AND a block record newly reached
 * status "accepted". A "pending" block (submitted, unverified) never
 * celebrates; it only sets `pending`.
 */
export function blockEvents(prev, next) {
  const out = { celebrate: [], pending: false, newPending: [] };
  if (!next) return out;
  const blocks = next.blocks || [];
  out.pending = (next.pool?.blocks_pending ?? 0) > 0 || blocks.some((b) => b.status === 'pending');
  if (!prev) return out; // first state after load: nothing is "new"
  const before = new Map((prev.blocks || []).map((b) => [b.hash, b.status]));
  const foundUp = (next.pool?.blocks_found ?? 0) - (prev.pool?.blocks_found ?? 0);
  for (const b of blocks) {
    const was = before.get(b.hash);
    if (b.status === 'pending' && was === undefined) out.newPending.push(b);
    if (foundUp > 0 && b.status === 'accepted' && was !== 'accepted') out.celebrate.push(b);
  }
  out.celebrate = out.celebrate.slice(0, Math.max(0, foundUp));
  return out;
}

/** Number of newly accepted shares (drives pickaxe swings). */
export function shareDelta(prev, next) {
  if (!prev || !next) return 0;
  return Math.max(0, (next.pool?.shares_accepted ?? 0) - (prev.pool?.shares_accepted ?? 0));
}

/** A job (round) that just completed, if any. */
export function finishedRound(prev, next) {
  if (!prev || !next) return null;
  const cur = (next.rounds || []).find((r) => r.current);
  const prevCur = (prev.rounds || []).find((r) => r.current);
  if (!prevCur || !cur || prevCur.prev_hash === cur.prev_hash) return null;
  return (next.rounds || []).find((r) => r.prev_hash === prevCur.prev_hash) || null;
}
