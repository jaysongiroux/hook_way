import { toBundle } from "./bundle";
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useShallow } from "zustand/react/shallow";
import type { FlowSnapshot, GraphDocument } from "./model";
import { yamlIdOf } from "./model";
import { useGraphStore } from "./store";
import { VALIDATOR_RETRY_EVENT } from "../shared/wasm/validationMode";
import { validateBundle, validateYaml, type ValidationIssue } from "./validate";

export type BuilderValidationStatus =
  | "checking"
  | "valid"
  | "invalid"
  | "unavailable";

interface BuilderDraftIssue {
  key: string;
  error: string;
}

export interface BuilderValidationValue {
  issues: ValidationIssue[];
  status: BuilderValidationStatus;
  draftIssues: BuilderDraftIssue[];
}

export interface LocatedIssue {
  flowId: string;
  nodeId?: string;
  path?: string;
}

const BuilderValidationContext = createContext<BuilderValidationValue | null>(
  null,
);

/** Provides debounced, authoritative validation for the current exported core YAML. */
export function BuilderValidationProvider({
  children,
}: {
  children: ReactNode;
}) {
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const retry = () => setRevision((value) => value + 1);
    window.addEventListener(VALIDATOR_RETRY_EVENT, retry);
    return () => window.removeEventListener(VALIDATOR_RETRY_EVENT, retry);
  }, []);
  const formDrafts = useGraphStore((state) => state.formDrafts);
  const documentParts = useGraphStore(
    useShallow((state) => ({
      yamlMode: state.yamlMode,
      entryFilename: state.entryFilename,
      flowFiles: state.flowFiles,
      nodes: state.nodes,
      edges: state.edges,
      serverConfig: state.serverConfig,
      coreReplayConfig: state.coreReplayConfig,
      configExtra: state.configExtra,
      connections: state.connections,
      destinations: state.destinations,
      activeFlow: state.activeFlow,
      otherFlows: state.otherFlows,
      deliveryFailurePolicy: state.deliveryFailurePolicy,
      flowExtra: state.flowExtra,
    })),
  );

  const exportResult = useMemo(() => {
    const document: GraphDocument = {
      yamlMode: documentParts.yamlMode,
      entryFilename: documentParts.entryFilename,
      flowFiles: documentParts.flowFiles,
      nodes: documentParts.nodes,
      edges: documentParts.edges,
      serverConfig: documentParts.serverConfig,
      coreReplayConfig: documentParts.coreReplayConfig,
      replayServiceConfig: null,
      configExtra: documentParts.configExtra,
      connections: documentParts.connections,
      destinations: documentParts.destinations,
      activeFlow: documentParts.activeFlow,
      otherFlows: documentParts.otherFlows,
      deliveryFailurePolicy: documentParts.deliveryFailurePolicy,
      flowExtra: documentParts.flowExtra,
    };
    try {
      const bundle = toBundle(document);
      return { yaml: bundle.files[bundle.entry], bundle, error: "" };
    } catch (error) {
      return { yaml: "", bundle: null, error: String(error) };
    }
  }, [documentParts]);

  const [issues, setIssues] = useState<ValidationIssue[]>([]);
  const [status, setStatus] = useState<BuilderValidationStatus>("checking");
  const generation = useRef(0);

  useEffect(() => {
    const requestGeneration = ++generation.current;
    setIssues(
      exportResult.error
        ? [
            {
              field: "configuration",
              message: exportResult.error,
              severity: "error",
            },
          ]
        : [],
    );
    setStatus(exportResult.error ? "invalid" : "checking");
    if (exportResult.error) return;

    const timer = window.setTimeout(() => {
      void (documentParts.yamlMode === "split" && exportResult.bundle ? validateBundle(exportResult.bundle) : validateYaml(exportResult.yaml))
        .then((result) => {
          if (generation.current !== requestGeneration) return;
          if (result === null) {
            setIssues([]);
            setStatus("unavailable");
            return;
          }
          setIssues(result);
          setStatus(
            result.some((issue) => issue.severity !== "warning")
              ? "invalid"
              : "valid",
          );
        })
        .catch(() => {
          if (generation.current !== requestGeneration) return;
          setIssues([]);
          setStatus("unavailable");
        });
    }, 250);

    return () => {
      window.clearTimeout(timer);
      generation.current += 1;
    };
  }, [exportResult, revision]);

  const draftIssues = useMemo(
    () =>
      Object.entries(formDrafts ?? {}).flatMap(([key, draft]) =>
        draft.error.trim() ? [{ key, error: draft.error }] : [],
      ),
    [formDrafts],
  );

  const value = useMemo(
    () => ({ issues, status, draftIssues }),
    [issues, status, draftIssues],
  );
  return (
    <BuilderValidationContext.Provider value={value}>
      {children}
    </BuilderValidationContext.Provider>
  );
}

export function useBuilderValidation(): BuilderValidationValue {
  const value = useContext(BuilderValidationContext);
  const drafts = useGraphStore((s) => s.formDrafts);
  return (
    value ?? {
      issues: [],
      status: "unavailable",
      draftIssues: Object.entries(drafts ?? {})
        .filter(([, d]) => d.error)
        .map(([key, d]) => ({ key, error: d.error })),
    }
  );
}

/** Maps a Go validator field path back to the editor flow, node, and form path. */
export function locateIssue(
  field: string,
  document: GraphDocument,
): LocatedIssue | undefined {
  if (!field.startsWith("flows.")) return undefined;
  const flowId = [document.activeFlow, ...Object.keys(document.otherFlows)]
    .sort((a, b) => b.length - a.length)
    .find((id) => {
      const prefix = `flows.${id}.nodes`;
      if (!field.startsWith(prefix)) return false;
      const boundary = field[prefix.length];
      return boundary === "[" || boundary === ".";
    });
  if (!flowId) return undefined;
  const nodesMarker = `flows.${flowId}.nodes`;
  const flow = flowSnapshot(flowId, document);
  if (!flow) return undefined;

  const nodeField = field.slice(nodesMarker.length);
  const indexed = /^\[(\d+)\](?:\.(.*))?$/.exec(nodeField);
  const dottedIndex = /^\.(\d+)(?:\.(.*))?$/.exec(nodeField);
  const indexMatch = indexed ?? dottedIndex;
  if (indexMatch) {
    const node = flow.nodes[Number(indexMatch[1])];
    return node
      ? { flowId, nodeId: node.id, path: normalizeIssuePath(indexMatch[2]) }
      : undefined;
  }

  const remainder = nodeField.startsWith(".") ? nodeField.slice(1) : nodeField;
  const node = [...flow.nodes]
    .sort((a, b) => yamlIdOf(b).length - yamlIdOf(a).length)
    .find(
      (candidate) =>
        remainder === yamlIdOf(candidate) ||
        remainder.startsWith(`${yamlIdOf(candidate)}.`),
    );
  if (!node) return undefined;
  const suffix = remainder.slice(yamlIdOf(node).length).replace(/^\./, "");
  return { flowId, nodeId: node.id, path: normalizeIssuePath(suffix) };
}

function normalizeIssuePath(path: string | undefined): string | undefined {
  if (!path) return undefined;
  return path.startsWith("options.") ? path.slice("options.".length) : path;
}

function flowSnapshot(
  flowId: string,
  document: GraphDocument,
): FlowSnapshot | undefined {
  if (flowId === document.activeFlow) {
    return {
      nodes: document.nodes,
      edges: document.edges,
      deliveryFailurePolicy: document.deliveryFailurePolicy,
      flowExtra: document.flowExtra,
    };
  }
  return document.otherFlows[flowId];
}
