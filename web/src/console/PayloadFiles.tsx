import React from "react";
import { Alert, Button, Group, Stack, Text, Title } from "@mantine/core";
import { capturedFiles, downloadFile, type CapturedFile } from "./filePayload";
import { requestError, result, type RecordDetailData, type Session } from "./session";

export function PayloadFiles({ record, session }: { record: RecordDetailData; session: Session }) {
  const [files, setFiles] = React.useState<CapturedFile[]>();
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const mounted = React.useRef(true);
  React.useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  async function load() {
    setBusy(true);
    setError("");
    try {
      const response = await session.client.GET("/v1/records/{id}/payload", { params: { path: { id: record.id } }, parseAs: "stream", signal: session.signal });
      const stream = result(response);
      if (!stream) throw new Error("The payload response was empty.");
      const blob = await new Response(stream).blob();
      if (!mounted.current || session.signal.aborted) return;
      const files = await capturedFiles(blob, record.kind === "inspection", record.artifacts.payload?.content_type || "", record.artifacts.payload?.filename);
      if (mounted.current && !session.signal.aborted) setFiles(files);
    } catch (caught) {
      if (mounted.current && !session.signal.aborted) setError(requestError(caught, "File information could not be loaded. Download the complete payload instead."));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }
  return <section aria-label="Captured files">
    <Group justify="space-between" mt="md" mb="xs">
      <Title order={3} size="h5">Files</Title>
      {files !== undefined && <Button size="xs" variant="subtle" onClick={() => setFiles(undefined)}>Close files</Button>}
    </Group>
    {files === undefined ? <>
      <Text size="sm" c="dimmed" mb="sm">Load file information from the captured payload, then download individual files without opening a viewer.</Text>
      <Button size="xs" variant="default" loading={busy} onClick={() => void load()}>Load file information</Button>
    </> : files.length ? <Stack gap="md">{files.map((file, i) => <section key={i} aria-label={`File ${file.field || file.filename}`}>
      <dl className="delivery-metadata">
        <div><dt>{file.generatedName ? "Download name" : "Filename"}</dt><dd className="identifier">{file.filename || "Not recorded"}</dd></div>
        {file.field && <div><dt>Form field</dt><dd className="identifier">{file.field}</dd></div>}
        <div><dt>Content type</dt><dd>{file.contentType || "Not recorded"}</dd></div>
        <div><dt>Size</dt><dd>{file.size.toLocaleString()} bytes</dd></div>
      </dl>
      {file.blob ? <Button size="xs" variant="default" aria-label={`Download ${file.filename || "file"}`} onClick={() => downloadFile(file.blob!, file.filename)}>Download file</Button> : <Text size="sm" c="dimmed">{file.unavailable}</Text>}
    </section>)}</Stack> : <Text size="sm" c="dimmed">No files were found in the captured body.</Text>}
    {error && <Alert mt="sm" color="red" title="Files unavailable">{error}</Alert>}
  </section>;
}
