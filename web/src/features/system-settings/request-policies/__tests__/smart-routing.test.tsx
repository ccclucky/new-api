import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRouter,
  createRootRoute,
  createMemoryHistory,
  RouterContextProvider,
} from "@tanstack/react-router";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  cleanup,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { api } from "@/lib/api";

import { SettingsPageProvider } from "../../components/settings-page-context";
import { SmartRoutingSection } from "../smart-routing-section";

let currentOptions: Record<string, string>;
let client: QueryClient;
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  currentOptions = {
    "smart_routing_setting.enabled": "false",
    "smart_routing_setting.virtual_model": "auto",
    "smart_routing_setting.base_url": "https://api.typesafe.ai",
    "smart_routing_setting.api_key": "sk-test",
    "smart_routing_setting.fallback_model": "gpt-5.1",
    "smart_routing_setting.timeout_ms": "2000",
  };
  vi.spyOn(api, "get").mockImplementation(async () => ({
    data: {
      success: true,
      message: "",
      data: Object.entries(currentOptions).map(([key, value]) => ({
        key,
        value,
      })),
    },
  }));
  vi.spyOn(api, "put").mockImplementation(async (_url, request) => {
    const { key, value } = request as { key: string; value: string };
    currentOptions[key] = value;
    return { data: { success: true, message: "" } };
  });
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.restoreAllMocks();
});
function show() {
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  function Workspace() {
    const [container, setContainer] = useState<HTMLDivElement | null>(null);
    return (
      <>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          <SmartRoutingSection />
        </SettingsPageProvider>
      </>
    );
  }
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <Workspace />
      </RouterContextProvider>
    </QueryClientProvider>,
  );
}

it("shows the saved smart routing settings as defaults", async () => {
  show();
  expect(
    await screen.findByRole("switch", { name: "Enable smart routing" }),
  ).not.toBeChecked();
  expect(screen.getByLabelText("Virtual model name")).toHaveValue("auto");
  expect(screen.getByLabelText("Fallback model")).toHaveValue("gpt-5.1");
});

it("saves only the changed decision timeout", async () => {
  show();
  const timeout = await screen.findByRole("spinbutton", {
    name: "Decision timeout (ms)",
  });
  fireEvent.change(timeout, { target: { value: "500" } });
  await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1));
  expect(vi.mocked(api.put).mock.calls[0][1]).toEqual({
    key: "smart_routing_setting.timeout_ms",
    value: "500",
  });
  expect(api.put).not.toHaveBeenCalledWith(
    "/api/option/",
    expect.objectContaining({ key: "smart_routing_setting.api_key" }),
  );
});

it("rejects an invalid decision base URL without saving", async () => {
  show();
  await userEvent.click(
    await screen.findByRole("switch", { name: "Enable smart routing" }),
  );
  const baseUrl = await screen.findByLabelText("Decision model base URL");
  await userEvent.clear(baseUrl);
  await userEvent.type(baseUrl, "not-a-url");
  await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
  expect(await screen.findByText("Invalid URL")).toBeVisible();
  expect(api.put).not.toHaveBeenCalled();
});
