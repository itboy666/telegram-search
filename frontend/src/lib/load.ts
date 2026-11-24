import { ref } from "vue";
export function useLoad<T>(initial: T, loader: () => Promise<T>) {
  const data = ref<any>(initial);
  const loading = ref(false);
  const error = ref("");
  let sequence = 0;
  async function load() {
    const n = ++sequence;
    loading.value = true;
    error.value = "";
    try {
      const r = await loader();
      if (n === sequence) data.value = r;
    } catch (e) {
      if (n === sequence) error.value = (e as Error).message;
    } finally {
      if (n === sequence) loading.value = false;
    }
  }
  return { data, loading, error, load };
}
