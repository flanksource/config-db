import { JsonView } from './JsonView';

interface Props {
  spec: any;
}

export function ScrapeConfigPanel({ spec }: Props) {
  if (!spec) {
    return <div class="p-8 text-center text-gray-400 text-sm">No scrape configuration available</div>;
  }

  return (
    <div class="p-4">
      <h3 class="text-sm font-semibold text-gray-700 mb-3">Scrape Configuration</h3>
      <div class="bg-gray-50 p-3 rounded border overflow-x-auto">
        <JsonView data={spec} />
      </div>
    </div>
  );
}
