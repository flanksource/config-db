interface Props {
  aliases?: string[];
}

export function AliasList({ aliases }: Props) {
  if (!aliases || aliases.length === 0) return null;
  return (
    <ul class="list-disc list-inside space-y-0.5 pl-1">
      {aliases.map((alias, i) => (
        <li key={i} class="text-xs text-gray-600 font-mono group flex items-center gap-1">
          <span class="flex-1 break-all">{alias}</span>
          <button
            class="text-gray-300 hover:text-gray-600 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 focus-visible:text-gray-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 transition-opacity shrink-0"
            aria-label={`Copy alias ${alias}`}
            title="Copy"
            onClick={(e) => {
              e.stopPropagation();
              navigator.clipboard.writeText(alias);
            }}
          >
            <iconify-icon icon="codicon:copy" class="text-sm" />
          </button>
        </li>
      ))}
    </ul>
  );
}
