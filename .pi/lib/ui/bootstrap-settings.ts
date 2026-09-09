/** Adapter for Pi's native SettingsList. Pi currently exposes no setting
 * registration API; keep the compatibility seam isolated and shape-checked.
 * No command interception, replacement panel, or host files are modified. */
const marker = Symbol.for("pi-swarm-bootstrap-settings-adapter");
const itemId = "pi-swarm-bootstrap-model";
export interface BootstrapSettingsAccess {
  current(): string;
  models(): string[];
  save(value: string): void;
  error(message: string): void;
}
export function installBootstrapSettings(SettingsList: any, access: BootstrapSettingsAccess): () => void {
  const proto = SettingsList.prototype;
  let state = proto[marker];
  if (!state) {
    state = { access: undefined as BootstrapSettingsAccess | undefined, original: proto.render };
    Object.defineProperty(proto, marker, { value: state });
    proto.render = function(width: number) {
      const active = state.access;
      // Only Pi's top-level settings list, not other extension lists/submenus.
      if (active && Array.isArray(this.items) && this.items.some((x: any) => x.id === "autocompact") && this.items.some((x: any) => x.id === "steering-mode") && !this.items.some((x: any) => x.id === itemId)) {
        const item = {
          id: itemId, label: "Bootstrap model", currentValue: active.current(),
          description: "Model for bootstrap agents. Use session model follows the current conversation model.",
          submenu: (_value: string, done: (value?: string) => void) => {
            const options = [...new Set(["Use session model", active.current(), ...active.models()])];
            return new SettingsList(options.map((value, i) => ({ id: String(i), label: value, currentValue: "", values: [""] })), 10, this.theme,
              (id: string) => {
                const value = options[Number(id)];
                try { active.save(value); done(value); } catch (error) { active.error(error instanceof Error ? error.message : String(error)); }
              }, () => done(), { enableSearch: true });
          },
        };
        this.items.unshift(item);
        if (this.filteredItems !== this.items && Array.isArray(this.filteredItems)) this.filteredItems.unshift(item);
      }
      return state.original.call(this, width);
    };
  }
  state.access = access;
  return () => { if (state.access === access) state.access = undefined; };
}
