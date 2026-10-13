import { expect, test } from "@playwright/test";
import { BuilderPage } from "./builder";

const config=`
state_stores:
  cluster:
    namespace: production
    postgres: {url: "\${DATABASE_URL}"}
flows:
  example:
    nodes:
      - {id: in, type: http_trigger, path: /example, methods: [POST]}
      - {id: quota, type: rate_limit, store: cluster, rpm: 60, burst: 5}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: public, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: done, type: respond, status: 202}
    edges: [in>quota, quota>body, body>public, public>env, env>done]
`;

for(const width of [1440,390]){
 test(`shared protection authors valid YAML and persists at ${width}px`,async({page},info)=>{
  await page.setViewportSize({width,height:900});
  const errors:string[]=[];page.on("pageerror",e=>errors.push(e.message));
  const builder=new BuilderPage(page);await builder.gotoReady();await builder.importYaml(config);
  await builder.selectNode("quota");const inspector=builder.inspector();
  await expect(inspector.getByRole("textbox",{name:"Protection store"})).toHaveValue("cluster");
  await inspector.getByRole("textbox",{name:"Rate-limiting strategy"}).click();
  await page.getByRole("option",{name:/Sliding-window counter/}).click();
  await inspector.getByLabel("Requests per window",{exact:true}).fill("25");
  await inspector.getByLabel("Window duration",{exact:true}).fill("1m");
  await page.screenshot({path:info.outputPath("quota.png"),animations:"disabled"});
  let dialog=await builder.openConfigItem("Protection stores");
  await expect(dialog.getByLabel("Deployment namespace")).toHaveValue("production");
  await expect(dialog.getByLabel("Connection URL")).toHaveValue("${DATABASE_URL}");
  await expect(dialog.getByText(/state-store migrate/)).toBeVisible();
  expect(await dialog.evaluate(e=>e.scrollWidth-e.clientWidth)).toBeLessThanOrEqual(1);
  await page.screenshot({path:info.outputPath("stores.png"),animations:"disabled"});
  dialog=await builder.openConfigItem("Export YAML");
  await expect(dialog.getByRole("button",{name:"Download core.yaml"})).toBeEnabled();
  await expect(dialog.locator("pre")).toContainText("strategy: sliding_window_counter");
  await expect(dialog.locator("pre")).toContainText("limit: 25");
  await expect(dialog.locator("pre")).not.toContainText("rpm:");
  await builder.closeDialog();await page.reload();await builder.selectNode("quota");
  await expect(inspector.getByLabel("Requests per window",{exact:true})).toHaveValue("25");
  expect(errors).toEqual([]);
 });
}
