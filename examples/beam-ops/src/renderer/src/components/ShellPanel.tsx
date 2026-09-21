import { useEffect, useRef, useState, useCallback, KeyboardEvent } from 'react';
import { rpcApi } from '../services/rpcApi';

interface ShellLine {
  id: number;
  kind: 'sent' | 'output' | 'meta';
  text: string;
}

let seq = 0;
const mkLine = (kind: ShellLine['kind'], text: string): ShellLine => ({ id: seq++, kind, text });

interface Props {
  workspaceId: string | null;
  targetUserId?: number | null;
}

export default function ShellPanel({ workspaceId, targetUserId }: Props) {
  const [lines, setLines] = useState<ShellLine[]>([]);
  const [input, setInput] = useState('');
  const [running, setRunning] = useState(false);
  const [elapsedMs, setElapsedMs] = useState<number | null>(null);
  const [history, setHistory] = useState<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState(-1);

  const bottomRef  = useRef<HTMLDivElement>(null);
  const inputRef   = useRef<HTMLInputElement>(null);
  const timerRef   = useRef<ReturnType<typeof setInterval> | null>(null);
  const startRef   = useRef<number>(0);
  // True while we're waiting for the first response line after a send
  const awaitingRef = useRef(false);

  const push = useCallback((l: ShellLine) => setLines((prev) => [...prev, l]), []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'nearest' });
  }, [lines, elapsedMs]);

  function startTimer() {
    startRef.current = Date.now();
    setElapsedMs(0);
    timerRef.current = setInterval(
      () => setElapsedMs(Date.now() - startRef.current),
      100
    );
  }

  function stopTimer() {
    if (timerRef.current) { clearInterval(timerRef.current); timerRef.current = null; }
    setElapsedMs(null);
    awaitingRef.current = false;
  }

  useEffect(() => {
    if (!workspaceId) return;
    const wsNum = parseInt(workspaceId, 10);
    if (isNaN(wsNum)) return;

    const label = targetUserId ? `user ${targetUserId}` : `workspace ${wsNum}`;
    setLines([mkLine('meta', label)]);
    setRunning(false);
    stopTimer();

    const offOutput = rpcApi.onOutput((text) => {
      // First output line after a send clears the elapsed timer
      if (awaitingRef.current) stopTimer();
      push(mkLine('output', text));
    });

    const offExit = rpcApi.onExit((code) => {
      setRunning(false);
      stopTimer();
      push(mkLine('meta', `exited (${code ?? '?'})`));
    });

    const offError = rpcApi.onError((msg) => {
      setRunning(false);
      stopTimer();
      push(mkLine('meta', `error: ${msg}`));
    });

    rpcApi.start(wsNum, targetUserId ?? undefined)
      .then(() => setRunning(true))
      .catch((err: unknown) =>
        push(mkLine('meta', `failed to start: ${err instanceof Error ? err.message : String(err)}`))
      );

    return () => {
      offOutput();
      offExit();
      offError();
      rpcApi.stop().catch(() => {});
      stopTimer();
    };
  }, [workspaceId, targetUserId, push]);

  const submit = useCallback(async () => {
    const cmd = input.trim();
    if (!cmd || !running) return;

    setInput('');
    setHistoryIdx(-1);
    setHistory((prev) => [cmd, ...prev.slice(0, 99)]);
    push(mkLine('sent', cmd));

    // Start elapsed timer; it stops when the first response line arrives
    awaitingRef.current = true;
    startTimer();

    try {
      await rpcApi.send(cmd);
    } catch (err) {
      stopTimer();
      push(mkLine('meta', `send error: ${err instanceof Error ? err.message : String(err)}`));
    }
  }, [input, running, push]);

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') { submit(); return; }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      const next = Math.min(historyIdx + 1, history.length - 1);
      setHistoryIdx(next); setInput(history[next] ?? '');
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      const next = historyIdx - 1;
      if (next < 0) { setHistoryIdx(-1); setInput(''); return; }
      setHistoryIdx(next); setInput(history[next] ?? '');
    }
  }

  const elapsedSec = elapsedMs !== null ? (elapsedMs / 1000).toFixed(1) : null;

  function handlePanelMouseDown(e: React.MouseEvent<HTMLDivElement>) {
    const target = e.target as HTMLElement;
    // Let clicks on output lines fall through so text selection works
    if (target.closest('.shell-line-output, .shell-line-sent, .shell-line-meta')) return;
    inputRef.current?.focus();
  }

  return (
    <div className="shell-panel" onMouseDown={handlePanelMouseDown}>

      <div className="shell-header">
        <span className="shell-header-label">REMOTE SHELL</span>
        <span className={`shell-header-status ${running ? 'shell-status-live' : 'shell-status-off'}`}>
          {running ? 'LIVE' : 'OFFLINE'}
        </span>
      </div>
      <div className="shell-webhook-row">
        <span className="shell-webhook-label">WEBHOOK</span>
        <span className="shell-webhook-url">http://localhost:8080</span>
      </div>

      <div className="shell-body">
        {lines.map((l) => (
          <div key={l.id} className={`shell-line shell-line-${l.kind}`}>
            {l.kind === 'sent' && <span className="shell-prompt-glyph">{'> '}</span>}
            {l.text}
          </div>
        ))}

        {elapsedSec !== null && (
          <div className="shell-line shell-line-meta shell-elapsed">
            {elapsedSec}s
          </div>
        )}

        <div className="shell-input-line">
          <span className="shell-prompt-glyph">{'> '}</span>
          <input
            ref={inputRef}
            className="shell-input"
            value={input}
            onChange={(e) => { setInput(e.target.value); setHistoryIdx(-1); }}
            onKeyDown={onKeyDown}
            disabled={!running}
            placeholder={!workspaceId ? 'no workspace' : !running ? 'connecting…' : ''}
            spellCheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
          />
        </div>

        <div ref={bottomRef} />
      </div>
    </div>
  );
}
