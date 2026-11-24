import { onBeforeUnmount, ref } from "vue";
import { api, ApiError } from "./api";

type Challenge = {
  id: string;
  token: string;
  url: string;
  expires_at: string;
};

/** Challenge credentials stay in memory and are never stored in browser storage. */
export function useTelegramChallenge(
  purpose: "login" | "bind",
  onSuccess: (result: any) => Promise<void> | void,
) {
  const challenge = ref<Challenge | null>(null);
  const state = ref<
    | "idle"
    | "creating"
    | "pending"
    | "consuming"
    | "expired"
    | "cancelled"
    | "complete"
  >("idle");
  const error = ref("");
  const checking = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let generation = 0;
  let disposed = false;

  function clearTimer() {
    if (timer) clearTimeout(timer);
    timer = undefined;
  }

  function schedule(version: number) {
    clearTimer();
    if (!disposed && state.value === "pending" && version === generation)
      timer = setTimeout(() => check(version), 2500);
  }

  async function check(version = generation) {
    const current = challenge.value;
    if (!current || disposed || version !== generation) return;
    if (checking.value) {
      schedule(version);
      return;
    }
    checking.value = true;
    try {
      const result = await api(
        `/user/api/telegram/challenges/${encodeURIComponent(current.id)}`,
        undefined,
        "GET",
        { "X-Telegram-Challenge-Token": current.token },
      );
      if (disposed || version !== generation) return;
      error.value = "";
      if (result.status === "confirmed") {
        state.value = "consuming";
        const session = await api(
          `/user/api/telegram/challenges/${encodeURIComponent(current.id)}/consume`,
          { token: current.token },
        );
        if (disposed || version !== generation) return;
        state.value = "complete";
        challenge.value = null;
        clearTimer();
        await onSuccess(session);
      } else if (
        ["expired", "used", "cancelled", "rejected"].includes(result.status)
      ) {
        state.value = ["cancelled", "rejected"].includes(result.status)
          ? "cancelled"
          : "expired";
        challenge.value = null;
        clearTimer();
      }
    } catch (e) {
      if (!disposed && version === generation) {
        error.value = (e as Error).message;
        if (
          e instanceof ApiError &&
          ([404, 410].includes(e.status) ||
            (state.value === "consuming" && [401, 403, 409].includes(e.status)))
        ) {
          state.value = "expired";
          challenge.value = null;
        } else if (state.value === "consuming") state.value = "pending";
        // A failed response is never interpreted as authorization.
      }
    } finally {
      checking.value = false;
      schedule(version);
    }
  }

  async function start() {
    clearTimer();
    const version = ++generation;
    state.value = "creating";
    error.value = "";
    challenge.value = null;
    try {
      const result = await api<Challenge>("/user/api/telegram/challenges", {
        purpose,
      });
      if (disposed || version !== generation) return;
      const url = new URL(result.url);
      if (url.protocol !== "https:" || url.hostname !== "t.me")
        throw new Error("机器人链接暂时不可用，请联系管理员。");
      challenge.value = result;
      state.value = "pending";
      schedule(version);
    } catch (e) {
      if (!disposed && version === generation) {
        error.value = (e as Error).message;
        state.value = "idle";
      }
    }
  }

  function cancel() {
    generation++;
    clearTimer();
    challenge.value = null;
    state.value = "idle";
    error.value = "";
  }

  onBeforeUnmount(() => {
    disposed = true;
    generation++;
    clearTimer();
    challenge.value = null;
  });

  return { challenge, state, error, checking, start, check, cancel };
}
