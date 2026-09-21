import { useState, useRef, useCallback } from 'react';
import { flushSync } from 'react-dom';
import { rpcApi } from '../services/rpcApi';

export interface ShellLine {
  id: number;
  kind: 'sent' | 'output' | 'meta';
  text: string;
}

export interface NodeSysInfo {
  hostname: string;
  cpu: string;
  os: string;
  firmwareVersion?: string;
}

export interface UseShellReturn {
  lines: ShellLine[];
  running: boolean;
  elapsed: number | null;
  input: string;
  setInput: (v: string) => void;
  connect: (wsNum: number, userId: number, label: string, onSysInfo: (info: NodeSysInfo) => void) => void;
  disconnect: () => void;
  submit: () => Promise<void>;
  interrupt: () => Promise<void>;
  clear: () => void;
  navigateHistory: (dir: 'up' | 'down') => void;
}

export function useShell(): UseShellReturn {
  const [lines, setLines] = useState<ShellLine[]>([]);
  const [running, setRunning] = useState(false);
  const [elapsed, setElapsed] = useState<number | null>(null);
  const [input, setInputState] = useState('');

  const seqRef = useRef(0);
  const sessionRef = useRef(0);
  const cleanupRef = useRef<(() => void) | null>(null);
  const runningRef = useRef(false);
  const inputRef = useRef('');
  const historyRef = useRef<string[]>([]);
  const histIdxRef = useRef(-1);
  const elapsedTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const mkLine = useCallback((kind: ShellLine['kind'], text: string): ShellLine => ({
    id: seqRef.current++,
    kind,
    text,
  }), []);

  const setInput = useCallback((v: string) => {
    inputRef.current = v;
    setInputState(v);
  }, []);

  const stopElapsed = useCallback(() => {
    if (elapsedTimerRef.current !== null) {
      clearInterval(elapsedTimerRef.current);
      elapsedTimerRef.current = null;
    }
    setElapsed(null);
  }, []);

  const startElapsed = useCallback(() => {
    if (elapsedTimerRef.current !== null) clearInterval(elapsedTimerRef.current);
    const start = Date.now();
    setElapsed(0);
    elapsedTimerRef.current = setInterval(() => {
      setElapsed(Date.now() - start);
    }, 100);
  }, []);

  const disconnect = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    sessionRef.current++;
    stopElapsed();
    rpcApi.stop().catch(() => {});
    runningRef.current = false;
    setRunning(false);
    setLines([]);
    setInput('');
    histIdxRef.current = -1;
  }, [setInput, stopElapsed]);

  const connect = useCallback((
    wsNum: number,
    userId: number,
    label: string,
    onSysInfo: (info: NodeSysInfo) => void,
  ) => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    rpcApi.stop().catch(() => {});
    stopElapsed();

    const session = ++sessionRef.current;

    setLines([mkLine('meta', `connecting to ${label}…`)]);
    runningRef.current = false;
    setRunning(false);
    setInput('');
    histIdxRef.current = -1;

    const offOutput = rpcApi.onOutput((text) => {
      if (sessionRef.current !== session) return;
      stopElapsed();
      const m = text.match(/^NODEINFO:([^|]*)\|([^|]*)\|(.*)$/);
      if (m) {
        onSysInfo({ hostname: m[1].trim(), cpu: m[2].trim(), os: m[3].trim() });
        return;
      }
      if (!text) return;
      setLines(prev => [...prev, mkLine('output', text)]);
    });

    const offExit = rpcApi.onExit((code) => {
      if (sessionRef.current !== session) return;
      stopElapsed();
      runningRef.current = false;
      setRunning(false);
      setLines(prev => [...prev, mkLine('meta', `exited (${code ?? '?'})`)]);
    });

    const offError = rpcApi.onError((msg) => {
      if (sessionRef.current !== session) return;
      stopElapsed();
      runningRef.current = false;
      setRunning(false);
      setLines(prev => [...prev, mkLine('meta', `error: ${msg}`)]);
    });

    cleanupRef.current = () => { offOutput(); offExit(); offError(); };

    rpcApi.start(wsNum, userId)
      .then(async () => {
        if (sessionRef.current !== session) return;
        runningRef.current = true;
        setRunning(true);
        await rpcApi.send(
          `printf 'NODEINFO:%s|%s|%s\\n' "$(hostname)" "$(uname -m)" "$(uname -s)"`
        ).catch(() => {});
      })
      .catch((err: unknown) => {
        if (sessionRef.current !== session) return;
        const msg = err instanceof Error ? err.message : String(err);
        setLines(prev => [...prev, mkLine('meta', `failed: ${msg}`)]);
      });
  }, [mkLine, setInput, startElapsed, stopElapsed]);

  const submit = useCallback(async () => {
    const cmd = inputRef.current.trim();
    if (!cmd || !runningRef.current) return;
    histIdxRef.current = -1;
    historyRef.current = [cmd, ...historyRef.current.slice(0, 99)];
    // flushSync forces React to paint the sent line + waiting state to the DOM
    // before the IPC call goes out. Without this, a fast response from the shell
    // arrives before React flushes setElapsed(0), collapsing both into one render.
    flushSync(() => {
      setInput('');
      setLines(prev => [...prev, mkLine('sent', cmd)]);
      startElapsed();
    });
    try {
      await rpcApi.send(cmd);
    } catch (err) {
      stopElapsed();
      setLines(prev => [...prev, mkLine('meta', `send error: ${err instanceof Error ? err.message : String(err)}`)]);
    }
  }, [mkLine, setInput, startElapsed, stopElapsed]);

  const interrupt = useCallback(async () => {
    if (!runningRef.current) return;
    try { await rpcApi.send('\x03'); } catch { /* ignore */ }
  }, []);

  const clear = useCallback(() => setLines([]), []);

  const navigateHistory = useCallback((dir: 'up' | 'down') => {
    const history = historyRef.current;
    if (dir === 'up') {
      const next = Math.min(histIdxRef.current + 1, history.length - 1);
      histIdxRef.current = next;
      setInput(history[next] ?? '');
    } else {
      const next = histIdxRef.current - 1;
      histIdxRef.current = next;
      setInput(next < 0 ? '' : (history[next] ?? ''));
    }
  }, [setInput]);

  return { lines, running, elapsed, input, setInput, connect, disconnect, submit, interrupt, clear, navigateHistory };
}
