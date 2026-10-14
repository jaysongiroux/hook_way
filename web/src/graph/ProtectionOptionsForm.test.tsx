import { useState } from "react";
import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { ProtectionOptionsForm } from "./ProtectionOptionsForm";
import { StateStoresPanel } from "./StateStoresPanel";
import { useGraphStore } from "./store";
import { nodeMeta } from "./nodeSchema";

beforeEach(() => useGraphStore.getState().clear());

test("strategy change clears inactive settings while retaining store and shared identity policy", () => {
 useGraphStore.getState().setConfigExtra({ state_stores: { cluster: {namespace:"prod",postgres:{url:"${DB_URL}"}} } });
 const changed=vi.fn();
 function Form(){const [value,setValue]=useState<Record<string,unknown>>({store:"cluster",policy_id:"customers",strategy:"token_bucket",rpm:60,burst:5,key_by:"source_ip"});return <MantineProvider><ProtectionOptionsForm nodeType="rate_limit" value={value} onChange={(next)=>{setValue(next);changed(next);}} scope="test" /></MantineProvider>;}
 render(<Form/>);
 expect(changed).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("combobox",{name:"Rate-limiting strategy"}));
 fireEvent.click(screen.getByRole("option",{name:/Sliding-window counter/}));
 expect(changed).toHaveBeenLastCalledWith({store:"cluster",policy_id:"customers",strategy:"sliding_window_counter",key_by:"source_ip",limit:100,window:"1m"});
 expect(screen.queryByLabelText(/Burst allowance/)).not.toBeInTheDocument();
 expect(screen.getByText(/weighted estimate/)).toBeInTheDocument();
});

test("store backend changes preserve namespace and unrelated imported settings", () => {
 useGraphStore.getState().setConfigExtra({state_stores:{cluster:{namespace:"prod",redis:{url:"${REDIS_URL}"},timeout:"300ms",future:true}}});
 render(<MantineProvider><StateStoresPanel opened onClose={()=>{}}/></MantineProvider>);
 fireEvent.click(screen.getByRole("combobox",{name:"Protection backend"}));
 fireEvent.click(screen.getByRole("option",{name:"PostgreSQL · shared"}));
 expect(useGraphStore.getState().configExtra.state_stores).toEqual({cluster:{namespace:"prod",postgres:{},timeout:"300ms",future:true}});
 expect(screen.getByText(/state-store migrate/)).toBeInTheDocument();
});

test("verified quota ports use generated stage metadata", () => {
 expect(nodeMeta("rate_limit",{stage:"verified"})).toMatchObject({input:"Verified",output:"Verified"});
 expect(nodeMeta("rate_limit",{})).toMatchObject({input:"RequestHead",output:"RequestHead"});
});

test("store renames and backend changes reconcile invalid drafts", () => {
 const entry={namespace:"prod",redis:{url:"${REDIS_URL}"}};
 useGraphStore.setState({configExtra:{state_stores:{old:entry}},formDrafts:{"state_stores/old|state_stores.old.redis.addrs":{text:"[",error:"Invalid JSON"}}});
 useGraphStore.getState().setConfigExtra({state_stores:{renamed:entry}});
 expect(Object.keys(useGraphStore.getState().formDrafts??{})).toEqual(["state_stores/renamed|state_stores.renamed.redis.addrs"]);
 useGraphStore.getState().setConfigExtra({state_stores:{renamed:{namespace:"prod",postgres:{}}}});
 expect(useGraphStore.getState().formDrafts).toEqual({});
});
