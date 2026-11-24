import { defineStore } from "pinia";
import { reactive, ref } from "vue";
import { api, csrf } from "./api";
export const useAuth = defineStore("auth", () => {
  const users = reactive<Record<string, any>>({ admin: null, user: null });
  const access = ref<{
    super: boolean;
    permissions: string[];
    fields: Record<string, { read: string[]; write: string[] }>;
    roles: { id: number; name: string }[];
  }>({ super: false, permissions: [], fields: {}, roles: [] });
  function setAccess(value?: typeof access.value) {
    access.value = value || {
      super: false,
      permissions: [],
      fields: {},
      roles: [],
    };
  }
  function can(permission: string) {
    return access.value.permissions.includes(permission);
  }
  function readField(module: string, field: string) {
    return access.value.fields[module]?.read.includes(field) ?? false;
  }
  function writeField(module: string, field: string) {
    return access.value.fields[module]?.write.includes(field) ?? false;
  }
  function writable(module: string, values: Record<string, any>) {
    return Object.fromEntries(
      Object.entries(values).filter(([key]) => writeField(module, key)),
    );
  }
  async function ensure(scope: string) {
    try {
      const r = await api(`/${scope}/api/me`);
      users[scope] = r.user;
      csrf[scope] = r.csrf_token;
      if (scope === "admin") setAccess(r.access);
      return true;
    } catch {
      users[scope] = null;
      delete csrf[scope];
      if (scope === "admin") setAccess();
      return false;
    }
  }
  async function login(scope: string, values: unknown, register = false) {
    const r = await api(
      `/${scope}/api/${register ? "register" : "login"}`,
      values,
    );
    users[scope] = r.user;
    csrf[scope] = r.csrf_token;
    if (scope === "admin") setAccess(r.access);
  }
  async function logout(scope: string) {
    await api(`/${scope}/api/logout`, {});
    users[scope] = null;
    delete csrf[scope];
    if (scope === "admin") setAccess();
  }
  return {
    users,
    access,
    can,
    readField,
    writeField,
    writable,
    ensure,
    login,
    logout,
  };
});
