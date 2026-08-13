import { useMemo } from 'preact/hooks';
import type { ExternalCost } from '../types';

export function CostTable({ entries, search }: { entries: ExternalCost[]; search?: string }) {
  const filtered = useMemo(() => {
    if (!search) return entries;
    const q = search.toLowerCase();
    return entries.filter(e => JSON.stringify(e).toLowerCase().includes(q));
  }, [entries, search]);
  return <div class="h-full overflow-auto p-4">
    {filtered.length === 0 ? <div class="p-8 text-center text-gray-400 text-sm">No external costs</div> :
      <table class="w-full text-sm bg-white border rounded">
        <thead class="bg-gray-50 text-left"><tr>
          <th class="px-3 py-2">Resource</th><th class="px-3 py-2">Service / SKU</th>
          <th class="px-3 py-2">Category</th><th class="px-3 py-2">Period</th>
          <th class="px-3 py-2 text-right">Billed</th><th class="px-3 py-2 text-right">Effective</th>
          <th class="px-3 py-2">Source</th>
        </tr></thead>
        <tbody>{filtered.map((cost, i) => <tr key={`${cost.source_key || ''}:${cost.source_record_id || i}`} class="border-t">
          <td class="px-3 py-2 font-mono text-xs">{cost.resource_id || cost.config_id || cost.external_config_id?.external_id || 'unmatched'}</td>
          <td class="px-3 py-2">{cost.service_name || '—'}{cost.sku_id ? <span class="text-gray-400"> / {cost.sku_id}</span> : null}</td>
          <td class="px-3 py-2">{cost.charge_category || 'Usage'}{cost.charge_class ? ` (${cost.charge_class})` : ''}</td>
          <td class="px-3 py-2 text-xs">{cost.charge_period_start} → {cost.charge_period_end}</td>
          <td class="px-3 py-2 text-right font-mono">{cost.billed_cost} {cost.billing_currency}</td>
          <td class="px-3 py-2 text-right font-mono">{cost.effective_cost} {cost.billing_currency}</td>
          <td class="px-3 py-2 font-mono text-xs">{cost.source_key || 'default'}</td>
        </tr>)}</tbody>
      </table>}
  </div>;
}
