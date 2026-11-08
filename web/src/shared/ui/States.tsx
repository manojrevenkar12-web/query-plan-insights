import type { ReactNode } from "react";
import { ApiError } from "../api/client";

export function ErrorState({ error, onRetry, what }: { error: Error; onRetry: () => void; what: string }) {
  const retryable = !(error instanceof ApiError) || error.retryable;
  return (
    <div className="state" role="alert">
      <p className="state__title">Could not load {what}.</p>
      <p className="state__body">{error.message}</p>
      {retryable && (
        <button className="button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="state">
      <p className="state__title">{title}</p>
      {children && <div className="state__body">{children}</div>}
      {action}
    </div>
  );
}
