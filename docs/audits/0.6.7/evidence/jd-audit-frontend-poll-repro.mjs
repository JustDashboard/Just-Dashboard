import { chromium } from "/home/ubuntu/Just-Dashboard-audit-0.6.7/frontend/node_modules/playwright/index.mjs"
const browser=await chromium.launch({headless:true})
const page=await browser.newPage()
await page.clock.install()
const pending=[]
await page.route("**/api/v1/**", async route=>{
  const path=new URL(route.request().url()).pathname.slice(7)
  if(path==="/audit/"){pending.push(route);return}
  const body=path==="/auth/session" ? {authenticated:true,capabilities:["read"],user:{id:1,username:"audit",role:"viewer"}} : path==="/dashboard/update" ? {current:"0.6.7",latest:"0.6.7",releases:[]} : []
  await route.fulfill({status:200,contentType:"application/json",body:JSON.stringify(body)})
})
await page.goto("http://127.0.0.1:3197/audit")
await page.waitForRequest(r=>r.url().includes("/audit/"),{timeout:1000}).catch(()=>{})
await page.clock.runFor(15001)
await new Promise(resolve=>setTimeout(resolve,300))
console.log("CONCURRENT_UNANSWERED_AUDIT_REQUESTS",pending.length)
const fulfill=(route,target)=>route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({entries:[{id:1,ts:new Date().toISOString(),username:"audit",action:"audit",target,success:true,status:200}],total:1})})
await fulfill(pending.at(-1),"NEWER_RESPONSE")
await page.getByText("NEWER_RESPONSE",{exact:true}).waitFor()
await fulfill(pending.at(-2),"OLDER_RESPONSE")
await page.getByText("OLDER_RESPONSE",{exact:true}).waitFor()
console.log("LATE_OLD_RESPONSE_REPLACED_NEWER",await page.getByText("OLDER_RESPONSE",{exact:true}).isVisible())
await browser.close()
