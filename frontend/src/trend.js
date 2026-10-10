// Split at missing observations: never draw an invented line across a data gap.
export function trendSegments(points, { width = 760, height = 150, padding = 18, max = 80 } = {}) {
  const segments = [];
  let current = [];
  for (const [index, point] of points.entries()) {
    if (!Number.isFinite(point.value) || point.value < 0) {
      if (current.length) segments.push(current);
      current = [];
      continue;
    }
    current.push([
      padding + index / Math.max(1, points.length - 1) * (width - padding * 2),
      height - 12 - point.value / max * (height - 24),
    ]);
  }
  if (current.length) segments.push(current);
  return segments;
}
