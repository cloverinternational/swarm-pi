import { it, expect, vi } from "vitest";
import { installBootstrapSettings } from "../../lib/ui/bootstrap-settings.ts";

it("adds one native settings row, saves submenu selection, and preserves original settings", () => {
  class List {
    filteredItems: any[]; theme = {};
    constructor(public items: any[], _max?: any, _theme?: any, public change?: any, public cancel?: any) { this.filteredItems = items; }
    render(_width: number) { return this.items.map(x => x.label); }
  }
  const save = vi.fn();
  const access = { current: () => "Use session model", models: () => ["provider/model"], save, error: vi.fn() };
  const dispose = installBootstrapSettings(List, access);
  const list = new List([{ id: "autocompact", label: "Auto-compact" }, { id: "steering-mode", label: "Steering mode" }]);
  expect(list.render(80)).toEqual(["Bootstrap model", "Auto-compact", "Steering mode"]);
  installBootstrapSettings(List, access); list.render(80);
  expect(list.items).toHaveLength(3);
  const done = vi.fn(); const submenu = list.items[0].submenu("", done);
  submenu.change("1"); expect(save).toHaveBeenCalledWith("provider/model"); expect(done).toHaveBeenCalledWith("provider/model");
  submenu.change("0"); expect(save).toHaveBeenLastCalledWith("Use session model");
  expect(new List([{ id: "other", label: "Other" }]).render(80)).toEqual(["Other"]);
  dispose();
  expect(new List([{ id: "autocompact" }, { id: "steering-mode" }]).items).toHaveLength(2);
});
