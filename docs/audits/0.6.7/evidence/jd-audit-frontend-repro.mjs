import { chromium } from "/home/ubuntu/Just-Dashboard-audit-0.6.7/frontend/node_modules/playwright/index.mjs"
const browser = await chromium.launch({headless:true})
const page = await browser.newPage()
const requests = []
const heldOtherReads = []
const now = new Date().toISOString()
const conn = {id:1,name:"audit",driver:"postgres",host:"localhost",port:5432,database:"audit",username:"audit",createdAt:now}
const columns = [{name:"id",type:"bigint",nullable:false},{name:"name",type:"text",nullable:false}]
await page.route("**/api/v1/**", async route => {
  const url = new URL(route.request().url()), path = url.pathname.slice(7)
  let body = []
  if(path === "/auth/session") body = {authenticated:true,capabilities:["read","service.control","file.write","destructive"],user:{id:1,username:"audit",role:"operator"}}
  if(path === "/dashboard/update") body = {current:"0.6.7",latest:"0.6.7",releases:[]}
  if(path === "/databases/") body = [conn]
  if(path === "/databases/drivers") body = [{id:"postgres",name:"Postgres",sql:true,ddl:true,filterOps:["eq"]}]
  if(path === "/databases/1/tables") body = [{schema:"public",name:"items",type:"table",estimatedRows:2},{schema:"public",name:"other",type:"table",estimatedRows:2}]
  if(path === "/databases/1/table") body = {columns,primaryKey:["id"],foreignKeys:[],indexes:[]}
  if(path === "/databases/1/browse") {
    if(url.searchParams.get("table")==="other"){heldOtherReads.push(route);return}
    const rows = url.searchParams.has("orderBy") ? [[2,"B"],[1,"A"]] : [[1,"A"],[2,"B"]]
    body = {columns:["id","name"],types:["bigint","text"],rows,rowCount:2,rowsAffected:0,duration:"1ms"}
  }
  if(path === "/databases/1/rows") { requests.push({method:route.request().method(),body:route.request().postDataJSON()}); body = {} }
  await route.fulfill({status:200,contentType:"application/json",body:JSON.stringify(body)})
})
page.on("pageerror", e => console.log("pageerror",e.message))
await page.goto("http://127.0.0.1:3197/databases?conn=1&schema=public&table=items")
await page.getByRole("checkbox",{name:"Select row 1",exact:true}).waitFor()
await page.getByRole("checkbox",{name:"Select row 1",exact:true}).click()
await Promise.all([page.waitForResponse(r=> r.url().includes("/browse?") && r.url().includes("orderBy=id")),page.getByTitle("Sort by id",{exact:true}).click()])
await page.getByRole("button",{name:"Delete 1",exact:true}).click()
await page.getByRole("dialog").getByRole("button",{name:"Delete 1 row",exact:true}).click()
await page.getByRole("dialog").waitFor({state:"hidden"})
console.log("SORT_DELETE",JSON.stringify(requests))
await page.getByRole("button",{name:"Insert",exact:true}).click()
await page.locator('[id="f-id"]').fill("9007199254740993")
await page.locator('[id="f-name"]').fill("precision")
await page.getByRole("dialog").getByRole("button",{name:"Insert",exact:true}).click()
await page.getByRole("dialog").waitFor({state:"hidden"})
console.log("BIGINT_INSERT",JSON.stringify(requests.at(-1)))
await page.getByRole("button",{name:"other table",exact:false}).click()
await page.waitForURL("**table=other")
await page.getByRole("checkbox",{name:"Select row 1",exact:true}).click()
await page.getByRole("button",{name:"Delete 1",exact:true}).click()
await page.getByRole("dialog").getByRole("button",{name:"Delete 1 row",exact:true}).click()
await page.getByRole("dialog").waitFor({state:"hidden"})
console.log("STALE_TABLE_DELETE_BEFORE_OTHER_READ",JSON.stringify(requests.at(-1)),"heldReads",heldOtherReads.length)
await browser.close()
