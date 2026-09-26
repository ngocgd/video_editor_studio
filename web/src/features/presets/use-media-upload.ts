import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { finalizeAssetMutation, presignAssetMutation } from "../../api/gen/@tanstack/react-query.gen";

const LIMITS = {
  image: { maxBytes: 25 * 1024 * 1024, mimes: ["image/png", "image/jpeg", "image/webp"] },
  audio: { maxBytes: 200 * 1024 * 1024, mimes: ["audio/wav", "audio/x-wav", "audio/flac", "audio/mpeg"] },
} as const;

export class MediaUploadError extends Error {}

/** Checks a file against the kind's allowlist and size cap before any request (the server re-checks by sniffing). */
export function validateMedia(kind: keyof typeof LIMITS, file: Pick<File, "size" | "type">): void {
  const limit = LIMITS[kind];
  if (file.size <= 0 || file.size > limit.maxBytes) {
    throw new MediaUploadError(`The file must be under ${Math.round(limit.maxBytes / 1024 / 1024)}MB.`);
  }
  if (!(limit.mimes as readonly string[]).includes(file.type)) {
    throw new MediaUploadError(kind === "image" ? "Only PNG, JPEG or WebP images." : "Only WAV, FLAC or MP3 audio.");
  }
}

/**
 * Presigned-POST upload of an image or audio file: presign -> POST the
 * file straight to storage -> finalize (which sniffs the bytes and
 * re-checks the size server-side). Returns the ready asset id.
 */
export function useMediaUpload(kind: keyof typeof LIMITS) {
  const presign = useMutation(presignAssetMutation());
  const finalize = useMutation(finalizeAssetMutation());
  const [busy, setBusy] = useState(false);

  const upload = async (file: File): Promise<string> => {
    validateMedia(kind, file);
    setBusy(true);
    try {
      const presigned = await presign.mutateAsync({ body: { kind, mime: file.type, bytes: file.size, filename: file.name } });
      if (!presigned) throw new MediaUploadError("Could not get an upload slot.");
      const form = new FormData();
      for (const [key, value] of Object.entries(presigned.fields)) form.append(key, value);
      form.append("file", file);
      const res = await fetch(presigned.uploadUrl, { method: "POST", body: form });
      if (!res.ok) throw new MediaUploadError(`Upload to storage failed (${res.status}).`);
      const asset = await finalize.mutateAsync({ path: { id: presigned.assetId } });
      if (!asset) throw new MediaUploadError("The upload could not be verified.");
      return asset.id;
    } finally {
      setBusy(false);
    }
  };
  return { upload, busy };
}
