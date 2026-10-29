// Go が返した error は、JavaScript では Error(message が Go のエラーの文)として届く
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// isUnreachable は、aibow-server につながらなかったエラーか。
// Go の api/client の ErrUnreachable と、ブラウザの backendWeb.ts が出す文は、どちらもこの文で始まる
export function isUnreachable(err: unknown): boolean {
  return errorMessage(err).includes("aibow-server につながりません");
}
