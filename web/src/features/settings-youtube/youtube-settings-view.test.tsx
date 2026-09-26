import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import type { YouTubeChannel, YouTubeQuota } from "../../api/gen/types.gen";
import { connectResultMessage, validateYouTubeSettingsSearch } from "./connect-result";
import { longUploadsText, visibilityText, YouTubeChannelCard } from "./youtube-channel-card";
import { YouTubeSettingsPanel } from "./youtube-settings-view";

function channel(overrides: Partial<YouTubeChannel> = {}): YouTubeChannel {
  return {
    id: "8f4c2d9e-1b3a-4c5d-9e8f-0a1b2c3d4e5f",
    youtubeChannelId: "UC1234567890",
    title: "Night Tales",
    thumbnailUrl: "",
    scopes: ["https://www.googleapis.com/auth/youtube.upload"],
    canUpload: true,
    apiProjectAudited: false,
    auditNote: "",
    longUploadsStatus: "eligible",
    customThumbnailsOk: false,
    status: "connected",
    createdAt: "2026-09-26T10:00:00Z",
    updatedAt: "2026-09-26T10:00:00Z",
    ...overrides,
  };
}

const quota: YouTubeQuota = { used: 1650, limit: 10_000, resetsAt: "2026-09-27T07:00:00Z" };

function panel(props: Partial<Parameters<typeof YouTubeSettingsPanel>[0]> = {}) {
  return (
    <YouTubeSettingsPanel
      channels={[]}
      oauthConfigured
      quota={quota}
      isOwner
      search={{}}
      onConnect={() => {}}
      channelCard={(c) => <p key={c.id}>{c.title}</p>}
      {...props}
    />
  );
}

describe("connect result", () => {
  it("keeps only known values from the callback query", () => {
    expect(validateYouTubeSettingsSearch({ connect: "error", reason: "<script>", channel: "x" })).toEqual({ connect: "error" });
    expect(validateYouTubeSettingsSearch({ connect: "maybe" })).toEqual({});
  });

  it("maps reason codes to fixed text and never echoes unknown ones", () => {
    expect(connectResultMessage({ connect: "ok" })).toEqual({ tone: "success", text: "YouTube channel connected." });
    expect(connectResultMessage({ connect: "error", reason: "upload_scope_missing" })?.text).toMatch(/upload permission/);
    expect(connectResultMessage({ connect: "error", reason: "made_up_code" })?.text).toBe("Connecting the channel failed. Try again.");
    expect(connectResultMessage({})).toBeNull();
  });
});

describe("YouTubeSettingsPanel", () => {
  it("disables connecting and explains why when OAuth is not configured", () => {
    render(panel({ oauthConfigured: false }));
    expect(screen.getByRole("note")).toHaveTextContent(/not configured/);
    expect(screen.getByRole("button", { name: "Connect a YouTube channel" })).toBeDisabled();
  });

  it("hides the connect button from non-owners", () => {
    render(panel({ isOwner: false }));
    expect(screen.queryByRole("button", { name: "Connect a YouTube channel" })).toBeNull();
    expect(screen.getByText(/Only the workspace owner/)).toBeInTheDocument();
  });

  it("starts the connect flow and shows the callback result", () => {
    const onConnect = vi.fn();
    render(panel({ onConnect, search: { connect: "error", reason: "consent_denied" } }));
    expect(screen.getByRole("alert")).toHaveTextContent(/consent was declined/);
    fireEvent.click(screen.getByRole("button", { name: "Connect a YouTube channel" }));
    expect(onConnect).toHaveBeenCalledOnce();
  });

  it("shows today's quota", () => {
    render(panel());
    expect(screen.getByRole("progressbar", { name: "YouTube API quota used today" })).toHaveAttribute("aria-valuenow", "1650");
  });

  it("has no obvious accessibility violations", async () => {
    const { container } = render(panel({ channels: [channel()], search: { connect: "ok" } }));
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe("YouTubeChannelCard", () => {
  it("states that uploads stay private until the API project is audited", () => {
    expect(visibilityText({ apiProjectAudited: false })).toMatch(/^Private \(API not audited\)/);
    expect(visibilityText({ apiProjectAudited: true })).not.toMatch(/Private \(API not audited\)/);
    expect(longUploadsText("eligible")).toMatch(/must be verified/);
  });

  it("shows the status and asks to reconnect a dead grant", () => {
    render(<YouTubeChannelCard channel={channel({ status: "reconnect_needed", canUpload: false })} isOwner onSaveAudit={() => {}} onDisconnect={() => {}} />);
    expect(screen.getByText("Reconnect needed")).toBeInTheDocument();
    expect(screen.getByText(/Connect the channel again/)).toBeInTheDocument();
  });

  it("saves the audit toggle with the form date and note", () => {
    const onSaveAudit = vi.fn();
    render(<YouTubeChannelCard channel={channel()} isOwner onSaveAudit={onSaveAudit} onDisconnect={() => {}} />);
    const save = screen.getByRole("button", { name: "Save audit status" });
    expect(save).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.change(screen.getByLabelText("Audit form submitted on"), { target: { value: "2026-09-20" } });
    fireEvent.change(screen.getByLabelText("Note"), { target: { value: "approved" } });
    fireEvent.click(save);
    expect(onSaveAudit).toHaveBeenCalledWith({ apiProjectAudited: true, auditFormDate: "2026-09-20", auditNote: "approved" });
  });

  it("disconnects only after confirmation", () => {
    const onDisconnect = vi.fn();
    render(<YouTubeChannelCard channel={channel()} isOwner onSaveAudit={() => {}} onDisconnect={onDisconnect} />);
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    expect(onDisconnect).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    fireEvent.click(Array.from(dialog.querySelectorAll("button")).find((b) => b.textContent === "Disconnect")!);
    expect(onDisconnect).toHaveBeenCalledOnce();
  });

  it("offers no owner actions to editors and viewers, or on a disconnected channel", () => {
    const { rerender } = render(<YouTubeChannelCard channel={channel()} isOwner={false} onSaveAudit={() => {}} onDisconnect={() => {}} />);
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
    rerender(<YouTubeChannelCard channel={channel({ status: "disconnected" })} isOwner onSaveAudit={() => {}} onDisconnect={() => {}} />);
    expect(screen.queryByRole("button", { name: "Save audit status" })).toBeNull();
  });
});
