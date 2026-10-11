// Go が返した error は、JavaScript では Error(message が Go のエラーの文)として届く
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
