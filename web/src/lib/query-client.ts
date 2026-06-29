import { QueryClient, QueryCache } from "@tanstack/react-query";
import { ApiError } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { router } from "@/router";

export const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err) => {
      if (err instanceof ApiError && err.status === 401) {
        useAuth.getState().clear();
        router.navigate({ to: "/login" });
      }
    },
  }),
});
