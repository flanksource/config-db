const ANSI_COLORS: Record<string, string> = {
  '30': 'color:#1e1e1e', '31': 'color:#cd3131', '32': 'color:#0dbc79',
  '33': 'color:#e5e510', '34': 'color:#2472c8', '35': 'color:#bc3fbc',
  '36': 'color:#11a8cd', '37': 'color:#e5e5e5',
  '90': 'color:#666', '91': 'color:#f14c4c', '92': 'color:#23d18b',
  '93': 'color:#f5f543', '94': 'color:#3b8eea', '95': 'color:#d670d6',
  '96': 'color:#29b8db', '97': 'color:#fff',
  '1': 'font-weight:bold', '2': 'opacity:0.7', '3': 'font-style:italic',
  '4': 'text-decoration:underline',
};

interface Span {
  text: string;
  style: string;
}

function parseAnsi(raw: string): Span[] {
  const spans: Span[] = [];
  const re = /\x1b\[([0-9;]*)m/g;
  let last = 0;
  let styles: string[] = [];
  let match;

  while ((match = re.exec(raw)) !== null) {
    if (match.index > last) {
      spans.push({ text: raw.slice(last, match.index), style: styles.join(';') });
    }
    const codes = match[1].split(';').filter(Boolean);
    for (const code of codes) {
      if (code === '0' || code === '') {
        styles = [];
      } else if (ANSI_COLORS[code]) {
        styles.push(ANSI_COLORS[code]);
      }
    }
    last = match.index + match[0].length;
  }

  if (last < raw.length) {
    spans.push({ text: raw.slice(last), style: styles.join(';') });
  }

  return spans;
}

interface Props {
  text: string;
  class?: string;
}

export function AnsiHtml({ text, class: className }: Props) {
  const spans = parseAnsi(text);
  return (
    <pre class={className}>
      {spans.map((s, i) =>
        s.style ? <span key={i} style={s.style}>{s.text}</span> : s.text
      )}
    </pre>
  );
}
