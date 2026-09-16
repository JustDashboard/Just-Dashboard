import { chromium } from "/home/ubuntu/Just-Dashboard-audit-0.6.7/frontend/node_modules/playwright/index.mjs"
const browser=await chromium.launch({headless:true})
const page=await browser.newPage()
const result=await page.evaluate(()=>{
  try {new Headers({"X-Confirm":"目录"}); return {success:true}}
  catch(error){return {success:false,error:error.message}}
})
console.log("UNICODE_CONFIRMATION_HEADER",JSON.stringify(result))
await browser.close()
