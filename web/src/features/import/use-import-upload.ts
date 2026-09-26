import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { finalizeAssetMutation, presignAssetMutation } from "../../api/gen/@tanstack/react-query.gen";

const MAX_BYTES = 10 * 1024 * 1024;
const ALLOWED_MIME = new Set(["text/plain", "text/markdown"]);

export class UploadValidationError extends Error {}

function validate(file: File): void {
  if (file.size > MAX_BYTES) {
    throw new UploadValidationError("File is over the 10MB limit.");
  }
  const isTextLike = ALLOWED_MIME.has(file.type) || /\.(txt|md)$/i.test(file.name);
  if (!isTextLike) {
    throw new UploadValidationError("Only .txt or .md manuscripts are supported.");
  }
}

/**
 * Presigned-POST upload for a manuscript (phase 6 import, kind=document,
 * .txt/.md, <=10MB): presign -> POST the file straight to storage -> finalize
 * (which MIME-sniffs and re-checks size server-side; see security checklist
 * "decoding errors reported, not guessed silently").
 */
export function useImportUpload() {
  const presign = useMutation(presignAssetMutation());
  const finalize = useMutation(finalizeAssetMutation());
  const [progressLabel, setProgressLabel] = useState<string | null>(null);

  const upload = async (file: File): Promise<string> => {
    validate(file);
    setProgressLabel("Requesting upload slot…");
    const presigned = await presign.mutateAsync({
      body: { kind: "document", mime: file.type || "text/plain", bytes: file.size, filename: file.name },
    });
    if (!presigned) throw new Error("Presign failed");

    setProgressLabel("Uploading…");
    const form = new FormData();
    for (const [key, value] of Object.entries(presigned.fields)) form.append(key, value);
    form.append("file", file);
    const uploadResponse = await fetch(presigned.uploadUrl, { method: "POST", body: form });
    if (!uploadResponse.ok) {
      throw new Error(`Upload to storage failed (${uploadResponse.status})`);
    }

    setProgressLabel("Verifying…");
    const asset = await finalize.mutateAsync({ path: { id: presigned.assetId } });
    setProgressLabel(null);
    if (!asset) throw new Error("Finalize failed");
    return asset.id;
  };

  return {
    upload,
    isUploading: presign.isPending || finalize.isPending,
    progressLabel,
    error: (presign.error ?? finalize.error) as Error | null,
  };
}
