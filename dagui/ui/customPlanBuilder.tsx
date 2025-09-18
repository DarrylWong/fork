import React, { useEffect, useMemo, useState, useRef } from "react";
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  Node,
  Edge,
  MarkerType,
} from "reactflow";
import "reactflow/dist/style.css";

/**
 * Custom Plan Builder (DAG UI) — JSON v3 (with hookID) — Standalone UI v8
 * --------------------------------------------------------------------
 * Changes in v8:
 *  - No submit button. UI only builds an ordered, grouped plan on the right.
 *  - Test picker at the top-left. "Load DAG" calls /api/dag?test=<name> to
 *    fetch the DAG from the standalone Go server (which shells out to roachtest).
 *  - Show JSON still reveals the double-nested orderedHookIDs payload.
 */

// ------------------------------ Types -------------------------------

type RawStep = { description: string; hookID: string };
// Chain := StepGroup[], StepGroup := RawStep[]

type StepPosition = { chainID: number; depth: number };

type Step = {
  key: string; // `${stageIndex}-${chainID}-${depth}-${idx}`
  stageIndex: number;
  hookID: string;
  description: string;
  position: StepPosition;
  groupId?: string; // optional grouping identifier for contiguous blocks
};

type StepGroup = Step[]; // same depth

type Chain = StepGroup[]; // depth increases with index

type Stage = {
  name: string;
  index: number;
  maxStepConcurrency: number;
  chains: Chain[];
};

type Dag = { stages: Stage[] };

// --------------------------- Sample JSON ----------------------------
// Used only when no test is provided or the server is unavailable.
const DEFAULT_RAW: any[] = [{"chains":[[[{"description":"install fixtures for version \"v24.2.2\"","hookID":"1"}],[{"description":"start cluster at version \"v24.2.2\"","hookID":"2"}],[{"description":"wait for all nodes (:1-4) to acknowledge cluster version '24.2' on system tenant","hookID":"3"}]]],"name":"setup"},{"chains":[[[{"description":"restart system server on node 1 with binary version master","hookID":"4"}]],[[{"description":"restart system server on node 2 with binary version master","hookID":"5"}]],[[{"description":"restart system server on node 3 with binary version master","hookID":"6"}]],[[{"description":"restart system server on node 4 with binary version master","hookID":"7"}]],[[{"description":"run backup","hookID":"8"}]],[[{"description":"test features","hookID":"9"}]]],"name":"upgrade cluster from \"v24.2.2\" to \"master\""},{"chains":[[[{"description":"restart system server on node 1 with binary version v24.2.2","hookID":"10"}]],[[{"description":"restart system server on node 2 with binary version v24.2.2","hookID":"11"}]],[[{"description":"restart system server on node 3 with binary version v24.2.2","hookID":"12"}]],[[{"description":"restart system server on node 4 with binary version v24.2.2","hookID":"13"}]],[[{"description":"run backup","hookID":"14"}]],[[{"description":"test features","hookID":"15"}]]],"name":"downgrade nodes :1-4 from \"master\" to \"v24.2.2\""},{"chains":[[[{"description":"restart system server on node 1 with binary version master","hookID":"16"},{"description":"restart system server on node 2 with binary version master","hookID":"17"},{"description":"restart system server on node 3 with binary version master","hookID":"18"},{"description":"restart system server on node 4 with binary version master","hookID":"19"}],[{"description":"wait for all nodes (:1-4) to acknowledge cluster version <current> on system tenant","hookID":"20"}]],[[{"description":"run backup","hookID":"21"}]],[[{"description":"test features","hookID":"22"}]]],"name":"upgrade cluster from \"v24.2.2\" to \"master\""}];

// ---------------------------- Utilities -----------------------------

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const id = setTimeout(() => reject(new Error("fetch timeout")), ms);
    p.then((v) => { clearTimeout(id); resolve(v); }, (e) => { clearTimeout(id); reject(e); });
  });
}

function normalizeRawToDag(raw: any[]): Dag {
  const stages: Stage[] = (raw || []).map((s, si) => ({
    name: String(s?.name ?? `Stage ${si}`),
    index: si,
    maxStepConcurrency: Number(s?.maxStepConcurrency ?? 0),
    chains: (s?.chains ?? []).map((chain: any[], chainID: number) =>
      (chain ?? []).map((group: any[], depth: number) =>
        (group ?? []).map((st: RawStep, idx: number) => ({
          key: `${si}-${chainID}-${depth}-${idx}`,
          stageIndex: si,
          hookID: String((st as any)?.hookID ?? ""),
          description: String((st as any)?.description ?? `step ${idx}`),
          position: { chainID, depth },
        }))
      )
    ),
  }));
  return { stages };
}

// Compute per-chain column widths based on the widest concurrent group.
function computeChainLayout(stage: Stage) {
  const NODE_WIDTH = 240; // consistent box width
  const PEER_SPACING = NODE_WIDTH + 56; // ensure no overlap among peers
  const INTER_CHAIN_GAP = 100; // padding between chain columns
  const Y_GAP = 160; // vertical distance between depths

  const chainMaxPeers = stage.chains.map((chain) =>
    Math.max(1, ...chain.map((group) => Math.max(1, group.length)))
  );

  const chainWidths = chainMaxPeers.map((maxPeers) => NODE_WIDTH + (maxPeers - 1) * PEER_SPACING + 40);
  const chainOffsets: number[] = [];
  let acc = 0;
  for (let i = 0; i < chainWidths.length; i++) {
    chainOffsets.push(acc);
    acc += chainWidths[i] + INTER_CHAIN_GAP;
  }

  return { NODE_WIDTH, PEER_SPACING, Y_GAP, chainWidths, chainOffsets };
}

function makeNodesAndEdges(stage: Stage, chosen: Step[]) {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const { NODE_WIDTH, PEER_SPACING, Y_GAP, chainWidths, chainOffsets } = computeChainLayout(stage);
  const chosenSet = new Set(chosen.map((s) => s.hookID));

  stage.chains.forEach((chain, chainID) => {
    const colWidth = chainWidths[chainID];
    const colLeft = chainOffsets[chainID];
    const colCenterX = colLeft + colWidth / 2;

    chain.forEach((group, depth) => {
      const groupWidth = Math.max(1, group.length);
      const baseY = depth * Y_GAP;
      const startOffset = -((groupWidth - 1) * PEER_SPACING) / 2;

      group.forEach((step, idx) => {
        const selected = chosenSet.has(step.hookID);
        const nodeId = `s-${stage.index}-c-${chainID}-d-${depth}-k-${step.key}`;

        // Outline-only by default; filled variant when selected.
        const baseStyle: React.CSSProperties = {
          width: NODE_WIDTH,
          padding: 8,
          borderRadius: 10,
          border: "2px solid #3b3b3b",
          background: "transparent",
          color: "#111",
          fontWeight: 600,
        };
        const selectedStyle: React.CSSProperties = {
          width: NODE_WIDTH,
          padding: 8,
          borderRadius: 10,
          border: "2px solid #4c8bf5",
          background: "#0f1115",
          color: "#eaeef2",
          boxShadow: "0 0 0 2px rgba(76,139,245,0.2)",
        };

        nodes.push({
          id: nodeId,
          position: { x: colCenterX + startOffset + idx * PEER_SPACING, y: baseY },
          data: { label: `${step.description} (#${step.hookID})`, step },
          type: "default",
          draggable: false,
          className: "cr-node",
          style: selected ? selectedStyle : baseStyle,
        });
      });

      if (depth < chain.length - 1) {
        const nextGroup = chain[depth + 1];
        group.forEach((from) => {
          nextGroup.forEach((to) => {
            edges.push({
              id: `e-${stage.index}-c-${chainID}-d-${depth}-k-${from.key}-to-${to.key}`,
              source: `s-${stage.index}-c-${chainID}-d-${depth}-k-${from.key}`,
              target: `s-${stage.index}-c-${chainID}-d-${depth + 1}-k-${to.key}`,
              markerEnd: { type: MarkerType.ArrowClosed },
            });
          });
        });
      }
    });
  });

  return { nodes, edges };
}

// No dependency restriction; only prevent duplicate picks
function isSelectable(step: Step, chosen: Step[]): boolean {
  return !chosen.some((s) => s.hookID === step.hookID);
}

function tintForGroup(groupId?: string) {
  if (!groupId) return { bg: '#fff', border: '#d1d5db', chipBg: '#f3f4f6' };
  const palette = [
    { bg: '#EEF6FF', border: '#4C8BF5', chipBg: '#DBEAFF' },
    { bg: '#FFF6E6', border: '#F5A623', chipBg: '#FFE8C2' },
    { bg: '#EAF7EE', border: '#34C759', chipBg: '#D7F3DF' },
    { bg: '#F6EAFA', border: '#A259FF', chipBg: '#EBDDFF' },
  ];
  let sum = 0;
  for (let i = 0; i < groupId.length; i++) sum = (sum + groupId.charCodeAt(i)) % 1024;
  const idx = sum % palette.length;
  return palette[idx];
}

// ------------------------------ UI ---------------------------------

export default function CustomPlanBuilder() {
  const [dag, setDag] = useState<Dag | null>(null);
  const [source, setSource] = useState<string>("loading");
  const [chosen, setChosen] = useState<Step[]>([]); // single ordered list across all stages
  const [showJSON, setShowJSON] = useState(false);
  const leftColRef = useRef<HTMLDivElement>(null);

  // test control
  const [testName, setTestName] = useState<string>(() => new URLSearchParams(location.search).get("test") || "");

  // grouping selection state
  const [selectedIndices, setSelectedIndices] = useState<Set<number>>(new Set());
  const lastSelectedRef = useRef<number | null>(null);

  // drag-reorder state
  const [dragRange, setDragRange] = useState<{ start: number; end: number } | null>(null);
  const [insertIndex, setInsertIndex] = useState<number | null>(null);
  const [dragging, setDragging] = useState<boolean>(false);

  async function fetchDagForTest(t: string) {
    if (!t) { setDag(normalizeRawToDag(DEFAULT_RAW)); setSource("sample"); return; }
    try {
      const res = await withTimeout(fetch(`/api/dag?test=${encodeURIComponent(t)}`, { cache: "no-store" }), 5 * 60_000);
      if (!res.ok) throw new Error(await res.text());
      const raw = await res.json();
      const stagesIn: any[] = Array.isArray(raw) ? raw : (raw?.stages ?? []);
      setDag(normalizeRawToDag(stagesIn));
      setSource(`server:${t}`);
      history.replaceState(null, "", `?test=${encodeURIComponent(t)}`);
    } catch (e) {
      console.warn("/api/dag failed — falling back to sample", e);
      setDag(normalizeRawToDag(DEFAULT_RAW));
      setSource("sample");
    }
  }

  // Initial load: if a test is in the URL, try loading from the server; else sample
  useEffect(() => {
    if (testName) fetchDagForTest(testName); else { setDag(normalizeRawToDag(DEFAULT_RAW)); setSource("sample"); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onNodeClick(step: Step) {
    setChosen((prev) => (isSelectable(step, prev) ? [...prev, { ...step }] : prev));
  }
  function undo() {
    setChosen((prev) => prev.slice(0, -1));
    setSelectedIndices((prev) => {
      const arr = Array.from(prev);
      const max = Math.max(-1, ...arr);
      const n = new Set(prev);
      if (max === chosen.length - 1) n.delete(max);
      return n;
    });
  }
  function clearAll() { setChosen([]); setSelectedIndices(new Set()); }
  function removeAt(idx: number) {
    setChosen((prev) => prev.filter((_, i) => i !== idx));
    setSelectedIndices((prev) => {
      const next = new Set<number>();
      prev.forEach((i) => { if (i < idx) next.add(i); else if (i > idx) next.add(i - 1); });
      return next;
    });
  }

  // --- Selection helpers ---
  function getGroupBounds(index: number): { start: number; end: number } {
    const gid = chosen[index]?.groupId;
    if (!gid) return { start: index, end: index };
    let s = index, e = index;
    while (s - 1 >= 0 && chosen[s - 1].groupId === gid) s--;
    while (e + 1 < chosen.length && chosen[e + 1].groupId === gid) e++;
    return { start: s, end: e };
  }

  function toggleSelectIndex(i: number, range = false) {
    setSelectedIndices((prev) => {
      const next = new Set(prev);
      if (range && lastSelectedRef.current !== null) {
        const a = Math.min(lastSelectedRef.current, i);
        const b = Math.max(lastSelectedRef.current, i);
        for (let k = a; k <= b; k++) {
          const { start, end } = getGroupBounds(k);
          for (let t = start; t <= end; t++) next.add(t);
        }
      } else {
        const { start, end } = getGroupBounds(i);
        let allSelected = true;
        for (let t = start; t <= end; t++) if (!next.has(t)) { allSelected = false; break; }
        for (let t = start; t <= end; t++) {
          if (allSelected) next.delete(t); else next.add(t);
        }
      }
      lastSelectedRef.current = i;
      return next;
    });
  }

  function areIndicesContiguous(indices: number[]): boolean {
    indices.sort((a, b) => a - b);
    for (let i = 1; i < indices.length; i++) if (indices[i] !== indices[i - 1] + 1) return false;
    return indices.length > 0;
  }

  function groupSelected() {
    const idxs = Array.from(selectedIndices).sort((a, b) => a - b);
    if (!areIndicesContiguous([...idxs])) { alert("Select a contiguous block to group"); return; }
    if (!idxs.length) return;
    const gid = `g-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
    setChosen((prev) => prev.map((s, i) => (idxs.includes(i) ? { ...s, groupId: gid } : s)));
    setSelectedIndices(new Set());
  }

  function ungroupSelected() {
    if (selectedIndices.size === 0) return;
    setChosen((prev) => prev.map((s, i) => (selectedIndices.has(i) ? { ...s, groupId: undefined } : s)));
    setSelectedIndices(new Set());
  }

  // Build double-nested payload: consecutive items with the same non-empty groupId are grouped.
  const orderedHookIDs: string[][] = useMemo(() => {
    const out: string[][] = [];
    let current: string[] = [];
    let curG: string | undefined = undefined;
    chosen.forEach((s) => {
      if (s.groupId) {
        if (curG && s.groupId === curG) {
          current.push(s.hookID);
        } else {
          if (current.length > 0) out.push(current);
          current = [s.hookID];
          curG = s.groupId;
        }
      } else {
        if (current.length > 0) { out.push(current); current = []; curG = undefined; }
        out.push([s.hookID]);
      }
    });
    if (current.length > 0) out.push(current);
    return out;
  }, [chosen]);

  // Ensure scroll wheel in a stage scrolls the LEFT column instead of doing nothing
  const handleStageWheel: React.WheelEventHandler<HTMLDivElement> = (e) => {
    if (leftColRef.current) {
      leftColRef.current.scrollBy({ top: e.deltaY, left: e.deltaX, behavior: "auto" });
      e.preventDefault();
      e.stopPropagation();
    }
  };

  // ---------- Drag & Drop (right panel) ----------
  function getDragBlockFor(index: number): { start: number; end: number } {
    // If contiguous selection includes index, drag that block
    if (selectedIndices.size > 0 && selectedIndices.has(index)) {
      const arr = Array.from(selectedIndices).sort((a, b) => a - b);
      if (areIndicesContiguous([...arr])) return { start: arr[0], end: arr[arr.length - 1] };
    }
    // Else, if item is in a group, drag the entire contiguous group run
    const gid = chosen[index]?.groupId;
    if (gid) {
      let s = index, e = index;
      while (s - 1 >= 0 && chosen[s - 1].groupId === gid) s--;
      while (e + 1 < chosen.length && chosen[e + 1].groupId === gid) e++;
      return { start: s, end: e };
    }
    // Else just the single item
    return { start: index, end: index };
  }

  function beginDrag(i: number, e: React.DragEvent) {
    const range = getDragBlockFor(i);
    setDragRange(range);
    setDragging(true);
    e.dataTransfer.effectAllowed = 'move';
    try {
      e.dataTransfer.setData('text/plain', String(i));
      // Improve reliability across browsers by setting a drag image
      e.dataTransfer.setDragImage(e.currentTarget as HTMLElement, 16, 16);
    } catch {}
  }

  function onDragOverItem(i: number, e: React.DragEvent<HTMLLIElement>) {
    if (!dragging) return;
    e.preventDefault();
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const before = e.clientY < rect.top + rect.height / 2;
    // Snap insertion to GROUP BOUNDARIES so we never split a group
    const { start, end } = getGroupBounds(i);
    const idx = before ? start : end + 1;
    setInsertIndex(idx);
  }

  function onDragOverEndZone(e: React.DragEvent<HTMLDivElement>) {
    if (!dragging) return;
    e.preventDefault();
    setInsertIndex(chosen.length);
  }

  // Allow dropping anywhere over the list (not just on individual items)
  function onDragOverList(e: React.DragEvent<HTMLOListElement>) {
    if (!dragging) return;
    e.preventDefault();
    const list = e.currentTarget as HTMLOListElement;
    const items = Array.from(list.querySelectorAll('li')) as HTMLElement[];
    const y = e.clientY;
    let idx = items.length;
    for (let i = 0; i < items.length; i++) {
      const rect = items[i].getBoundingClientRect();
      if (y < rect.top + rect.height / 2) {
        // Snap to group boundary so we never split a group
        const { start } = getGroupBounds(i);
        idx = start;
        break;
      }
    }
    setInsertIndex(idx);
  }

  function finishDrop() {
    setDragging(false);
    setInsertIndex(null);
    setDragRange(null);
  }

  function onDropList(e: React.DragEvent) {
    e.preventDefault();
    if (!dragRange || insertIndex === null) { finishDrop(); return; }
    const { start, end } = dragRange;
    // If dropping inside the dragged block, no-op
    if (insertIndex >= start && insertIndex <= end + 1) { finishDrop(); return; }

    const block = chosen.slice(start, end + 1);
    const remaining = [...chosen.slice(0, start), ...chosen.slice(end + 1)];
    let idx = insertIndex;
    if (idx > start) idx -= (end - start + 1);
    const newChosen = [...remaining.slice(0, idx), ...block, ...remaining.slice(idx)];
    setChosen(newChosen);

    // Preserve selection if it exactly matched the dragged block; else clear
    const sel = Array.from(selectedIndices).sort((a, b) => a - b);
    const selIsBlock = sel.length === block.length && sel[0] === start && sel[sel.length - 1] === end && areIndicesContiguous([...sel]);
    if (selIsBlock) {
      const newSet = new Set<number>();
      for (let k = 0; k < block.length; k++) newSet.add(idx + k);
      setSelectedIndices(newSet);
    } else {
      setSelectedIndices(new Set());
    }

    finishDrop();
  }

  function onDragEnd() { finishDrop(); }

  if (!dag) {
    return (
      <div style={{ padding: 16, fontFamily: "system-ui" }}>
        <Banner source={source} />
        <p>Loading DAG…</p>
      </div>
    );
  }

  return (
    <div style={{ width: "100%", height: "88vh", display: "grid", gridTemplateColumns: "2fr 1fr", gap: 16, padding: 16, fontFamily: "system-ui" }}>
      <style>{`
      .cr-btn { appearance:none; border:1px solid #d1d5db; background:#fff; color:#111; font-weight:600; border-radius:10px; padding:8px 12px; cursor:pointer; transition:transform .06s ease, box-shadow .2s ease, background-color .2s ease, border-color .2s ease; }
      .cr-btn:hover:not(:disabled) { transform: translateY(-1px); box-shadow:0 6px 16px rgba(0,0,0,.08); }
      .cr-btn:active:not(:disabled) { transform: translateY(0); box-shadow:none; }
      .cr-btn:disabled { opacity:.5; cursor:not-allowed; box-shadow:none; }
      .cr-btn--sm { padding:6px 10px; font-size:12px; border-radius:8px; }
      .cr-btn--block { width:100%; padding-top:10px; padding-bottom:10px; }
      .cr-btn--primary { background:#2563eb; color:#fff; border-color:#1d4ed8; }
      .cr-btn--primary:hover:not(:disabled) { box-shadow:0 8px 24px rgba(37,99,235,.35); }
      .cr-btn--secondary { background:#fff; color:#111; border-color:#d1d5db; }
      .cr-btn--secondary:hover:not(:disabled) { border-color:#9ca3af; background:#fafafa; }
      .cr-btn--ghost { background:transparent; border-color:transparent; color:#374151; }
      .cr-btn--ghost:hover:not(:disabled) { background:#f3f4f6; }
      .cr-btn--danger { background:#ef4444; color:#fff; border-color:#dc2626; }
      .cr-btn--danger:hover:not(:disabled) { box-shadow:0 8px 24px rgba(239,68,68,.35); }

      /* React Flow node hover affordance */
      .react-flow__node.cr-node { transition: box-shadow .2s ease, transform .05s ease, background-color .2s ease, border-color .2s ease; cursor:pointer; }
      .react-flow__node.cr-node:hover { background:#fafafa !important; border-color:#6b7280 !important; }
      .react-flow__node.cr-node:active { transform: translateY(1px); }
      `}</style>
      {/* LEFT: all stages (own scroll) */}
      <div ref={leftColRef} style={{ display: "flex", flexDirection: "column", gap: 12, height: "100%", overflow: "auto" }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <input
            placeholder="roachtest name, e.g. pkg/acceptance/version-mixed"
            value={testName}
            onChange={(e) => setTestName(e.target.value)}
            style={{ flex: 1, padding: '8px 10px', borderRadius: 8, border: '1px solid #d1d5db' }}
          />
          <button className="cr-btn cr-btn--primary cr-btn--sm" onClick={() => fetchDagForTest(testName)} disabled={!testName.trim()}>Load DAG</button>
          <button className="cr-btn cr-btn--secondary cr-btn--sm" onClick={() => { setTestName(""); setDag(normalizeRawToDag(DEFAULT_RAW)); setSource('sample'); history.replaceState(null, '', location.pathname); }}>Use sample</button>
        </div>
        <Banner source={source} />

        {dag.stages.map((stage) => {
          const { nodes, edges } = makeNodesAndEdges(stage, chosen);
          return (
            <div key={stage.index} style={{ border: "1px solid #222", borderRadius: 12, padding: 12 }}>
              <div style={{ marginBottom: 8 }}><b>Stage {stage.index}:</b> <span style={{ color: "#9aa0a6" }}>{stage.name}</span></div>
              <div style={{ height: 380, border: "1px solid #333", borderRadius: 12 }} onWheelCapture={handleStageWheel}>
                <ReactFlow
                  nodes={nodes}
                  edges={edges}
                  onNodeClick={(_, node) => onNodeClick(((node.data as any).step as Step))}
                  nodesDraggable={false}
                  fitView
                  fitViewOptions={{ padding: 0.2 }}
                  style={{ background: '#fff' }}
                  zoomOnScroll={false}
                  zoomOnPinch={false}
                  zoomOnDoubleClick={false}
                  panOnScroll={false}
                >
                  <MiniMap pannable zoomable />
                  <Controls position="top-right" />
                  <Background />
                </ReactFlow>
              </div>
            </div>
          );
        })}
      </div>

      {/* RIGHT: single ordered list (own scroll, with selection + grouping + drag reorder) */}
      <div style={{ border: "1px solid #222", borderRadius: 12, padding: 12, height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 8 }}>
          <b>Ordered Plan</b>
          <div style={{ display: "flex", gap: 8 }}>
            <button className="cr-btn cr-btn--secondary cr-btn--sm" onClick={undo} disabled={!chosen.length}>Undo</button>
            <button className="cr-btn cr-btn--secondary cr-btn--sm" onClick={clearAll} disabled={!chosen.length}>Clear</button>
            <button className="cr-btn cr-btn--ghost cr-btn--sm" onClick={() => setShowJSON((v) => !v)}>{showJSON ? "Hide JSON" : "Show JSON"}</button>
          </div>
        </div>

        {/* Group actions */}
        <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
          <button className="cr-btn cr-btn--primary cr-btn--sm" onClick={groupSelected} disabled={selectedIndices.size === 0}>Group selected</button>
          <button className="cr-btn cr-btn--secondary cr-btn--sm" onClick={ungroupSelected} disabled={selectedIndices.size === 0}>Ungroup selected</button>
          <span style={{ color: '#9aa0a6', fontSize: 12 }}>{selectedIndices.size} selected</span>
        </div>

        <div style={{ flex: 1, overflow: "auto", display: "flex", flexDirection: "column", gap: 8 }}>
          {chosen.length === 0 ? (
            <div style={{ color: "#9aa0a6", fontSize: 13 }}>(no steps yet — click nodes to append)</div>
          ) : (
            <ol style={{ display: "flex", flexDirection: "column", gap: 8, paddingLeft: 16, margin: 0 }} onDrop={onDropList} onDragOver={onDragOverList}>
              {chosen.map((s, i) => {
                const selected = selectedIndices.has(i);
                const prevG = chosen[i-1]?.groupId;
                const nextG = chosen[i+1]?.groupId;
                const isGrouped = !!s.groupId;
                const isStart = isGrouped && s.groupId !== prevG;
                const isEnd = isGrouped && s.groupId !== nextG;
                const tint = tintForGroup(s.groupId);
                const style: React.CSSProperties = {
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  border: "1px solid #d1d5db",
                  borderRadius: 10,
                  padding: "8px 12px",
                  background: isGrouped ? tint.bg : "#fff",
                  borderLeft: isGrouped ? `6px solid ${tint.border}` : "1px solid #d1d5db",
                  outline: selected ? "2px solid #4c8bf5" : "none",
                  marginTop: isStart ? 6 : 0,
                  marginBottom: isEnd ? 6 : 0,
                  borderTopLeftRadius: isStart ? 10 : (isGrouped ? 0 : 10),
                  borderTopRightRadius: isStart ? 10 : (isGrouped ? 0 : 10),
                  borderBottomLeftRadius: isEnd ? 10 : (isGrouped ? 0 : 10),
                  borderBottomRightRadius: isEnd ? 10 : (isGrouped ? 0 : 10),
                  cursor: 'grab',
                  WebkitUserDrag: 'element',
                };
                const showInsertLineBefore = insertIndex === i;
                return (
                  <React.Fragment key={`${s.key}-${i}`}>
                    {showInsertLineBefore && (
                      <div style={{ height: 0, borderTop: '2px solid #4c8bf5', margin: '2px 4px' }} />
                    )}
                    <li
                      style={style}
                      onClick={(e) => toggleSelectIndex(i, e.shiftKey)}
                      draggable
                      onDragStart={(e) => beginDrag(i, e)}
                      onDragOver={(e) => onDragOverItem(i, e)}
                      onDragEnd={onDragEnd}
                    >
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>{isGrouped && <span style={{ fontSize: 11, padding: '2px 6px', borderRadius: 999, background: tint.chipBg, border: `1px solid ${tint.border}`, color: '#111' }}>Group</span>}
                        <div>
                          <div style={{ fontWeight: 600 }}>{s.description}</div>
                          <div style={{ color: "#6b7280", fontSize: 12 }}>hookID: {s.hookID}{s.groupId ? ` · group ${s.groupId}` : ""}</div>
                        </div>
                      </div>
                      <button className="cr-btn cr-btn--danger cr-btn--sm" onClick={(e) => { e.stopPropagation(); removeAt(i); }}>Remove</button>
                    </li>
                  </React.Fragment>
                );
              })}
              {insertIndex === chosen.length && (
                <div style={{ height: 0, borderTop: '2px solid #4c8bf5', margin: '2px 4px' }} onDragOver={onDragOverEndZone} />
              )}
            </ol>
          )}
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "1fr", gap: 8 }}>
          {showJSON && (
            <textarea readOnly value={JSON.stringify({ orderedHookIDs }, null, 2)} rows={8} style={{ width: "100%" }} />
          )}
        </div>
      </div>
    </div>
  );
}

function Banner({ source }: { source: string }) {
  const msg = source === "loading" ? "Set a test above, then Load DAG…" :
    source.startsWith("server:") ? `Loaded from server for ${source.slice(7)}` :
      source === "sample" ? "Using bundled sample JSON" : source;
  const good = source.startsWith("server:");
  const bg = good ? "#113116" : source === "sample" ? "#203133" : "#332018";
  const color = good ? "#9FE29F" : source === "sample" ? "#9FE2E2" : "#F2B8B5";
  return (
    <div style={{ background: bg, color, padding: 6, borderRadius: 8, fontSize: 12, marginBottom: 8 }}>
      {msg}
    </div>
  );
}
