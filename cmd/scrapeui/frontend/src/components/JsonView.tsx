import { useState } from 'preact/hooks';

interface Props {
  data: any;
  name?: string;
  depth?: number;
}

export function JsonView({ data, name, depth = 0 }: Props) {
  const [open, setOpen] = useState(depth < 2);

  if (data === null || data === undefined) {
    return <span class="text-gray-400 italic">null</span>;
  }

  if (typeof data === 'string') {
    return <span class="text-green-700">"{data}"</span>;
  }

  if (typeof data === 'number' || typeof data === 'boolean') {
    return <span class="text-blue-700">{String(data)}</span>;
  }

  const isArray = Array.isArray(data);
  const entries = isArray ? data.map((v: any, i: number) => [i, v]) : Object.entries(data);
  const bracket = isArray ? ['[', ']'] : ['{', '}'];

  if (entries.length === 0) {
    return <span class="text-gray-400">{bracket[0]}{bracket[1]}</span>;
  }

  return (
    <div class="text-sm font-mono" style={{ paddingLeft: depth > 0 ? '12px' : '0' }}>
      <span
        class="cursor-pointer hover:bg-gray-100 rounded px-0.5 select-none"
        onClick={() => setOpen(!open)}
      >
        <span class="text-gray-400 text-xs mr-1">{open ? '▼' : '▶'}</span>
        {name && <span class="text-purple-600">{name}</span>}
        {name && <span class="text-gray-400">: </span>}
        {!open && <span class="text-gray-400">{bracket[0]} {entries.length} {isArray ? 'items' : 'keys'} {bracket[1]}</span>}
        {open && <span class="text-gray-400">{bracket[0]}</span>}
      </span>
      {open && (
        <>
          {entries.map(([key, val]: [any, any]) => (
            <div key={key} class="pl-3 border-l border-gray-200 ml-1">
              {typeof val === 'object' && val !== null ? (
                <JsonView data={val} name={String(key)} depth={depth + 1} />
              ) : (
                <div>
                  <span class="text-purple-600">{isArray ? '' : String(key)}</span>
                  {!isArray && <span class="text-gray-400">: </span>}
                  <JsonView data={val} depth={depth + 1} />
                </div>
              )}
            </div>
          ))}
          <span class="text-gray-400">{bracket[1]}</span>
        </>
      )}
    </div>
  );
}
