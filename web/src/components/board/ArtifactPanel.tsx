import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-client";
import type { EpicArtifactResponse } from "@/lib/contracts";

// Presentational markdown viewer for a runner artifact (plan or review).
// The caller injects the query (key + fn returning {path,ref,markdown}) and the
// GitHub fallback URL; ArtifactPanel owns the loading/error/render states and an
// optional footer (e.g. the plan's "Approve plan" button). Reviewing from a
// phone is the whole point — real markdown, not a <pre>.
export function ArtifactPanel({ queryKey, queryFn, branchUrl, children }: {
  queryKey: readonly unknown[];
  queryFn: () => Promise<EpicArtifactResponse>;
  branchUrl: string;
  children?: ReactNode;
}) {
  const q = useQuery({ queryKey, queryFn, staleTime: 30_000, retry: false });

  if (q.isLoading) return <div className="text-xs text-muted-foreground">Loading…</div>;
  if (q.isError) {
    return (
      <div className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
        <div>{q.error instanceof ApiError ? q.error.message : "Couldn't load the artifact."}</div>
        <a href={branchUrl} target="_blank" rel="noreferrer" className="mt-1 inline-block text-primary underline">
          View the branch on GitHub ↗
        </a>
      </div>
    );
  }
  if (!q.data) return null;
  return (
    <>
      <div className="font-mono text-[11px] text-muted-foreground">{q.data.path} @ {q.data.ref}</div>
      <div className="markdown max-h-[50vh] overflow-y-auto rounded-md border border-border bg-background p-3">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{q.data.markdown}</ReactMarkdown>
      </div>
      {children}
    </>
  );
}
