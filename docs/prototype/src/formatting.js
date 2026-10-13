// Display formatting only: canonical timestamps, date keys and numeric facts stay unchanged.
const usd = new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'USD' });
const timestamp = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' });
export function formatCurrency(value) {
  return typeof value === 'number' && Number.isFinite(value) ? usd.format(value) : '金额未记录';
}
export function formatTimestamp(value) {
  if (!value) return '时间未记录';
  // Legacy activity entries may contain a time of day without a recorded date.
  if (/^\d{1,2}:\d{2}(?::\d{2})?$/.test(value)) return value;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? timestamp.format(date) : '时间无效';
}
