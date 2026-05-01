import { useState, useRef, useCallback } from 'preact/hooks';
import type { ComponentChildren } from 'preact';

interface Props {
  left: ComponentChildren;
  right: ComponentChildren;
  defaultSplit?: number;
  minLeft?: number;
  minRight?: number;
}

export function SplitPane({ left, right, defaultSplit = 50, minLeft = 20, minRight = 20 }: Props) {
  const [split, setSplit] = useState(defaultSplit);
  const dragging = useRef(false);
  const container = useRef<HTMLDivElement>(null);

  const onMouseDown = useCallback((e: MouseEvent) => {
    e.preventDefault();
    dragging.current = true;

    const onMove = (e: MouseEvent) => {
      if (!dragging.current || !container.current) return;
      const rect = container.current.getBoundingClientRect();
      const pct = ((e.clientX - rect.left) / rect.width) * 100;
      setSplit(Math.max(minLeft, Math.min(100 - minRight, pct)));
    };

    const onUp = () => {
      dragging.current = false;
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, [minLeft, minRight]);

  return (
    <div ref={container} class="flex flex-1 overflow-hidden">
      <div style={{ width: `${split}%` }} class="overflow-y-auto bg-white">
        {left}
      </div>
      <div
        class="w-1 bg-gray-200 hover:bg-blue-400 cursor-col-resize shrink-0 transition-colors"
        onMouseDown={onMouseDown}
      />
      <div style={{ width: `${100 - split}%` }} class="overflow-y-auto">
        {right}
      </div>
    </div>
  );
}
