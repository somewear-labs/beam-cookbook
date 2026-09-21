export const rpcApi = {
  start: (workspaceId: number, targetUserId?: number): Promise<void> =>
    window.rpcApi.start(workspaceId, targetUserId),

  send: (command: string): Promise<void> =>
    window.rpcApi.send(command),

  stop: (): Promise<void> =>
    window.rpcApi.stop(),

  onOutput: (cb: (line: string) => void): (() => void) =>
    window.rpcApi.onOutput(cb),

  onExit: (cb: (code: number | null) => void): (() => void) =>
    window.rpcApi.onExit(cb),

  onError: (cb: (msg: string) => void): (() => void) =>
    window.rpcApi.onError(cb)
};
