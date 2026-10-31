import {act,cleanup,fireEvent,render,screen} from "@testing-library/react";
import {afterEach,expect,it,vi} from "vitest";
import {CalendarStatus} from "./calendar-status";
import {LocaleProvider} from "./i18n";
afterEach(()=>{cleanup();vi.restoreAllMocks();localStorage.clear();});
it("checks saved sources explicitly and describes stale cache without reporting lost access",async()=>{
 localStorage.setItem("homedash.ui.locale","ru");
 const fetcher=vi.spyOn(globalThis,"fetch").mockImplementation(async(input)=>new Response(JSON.stringify(String(input).endsWith("calendar-status")?{date:"2026-10-31",complete:false,reason:"cache_stale",sources:[{id:"one",state:"ready",stale:true,reason:"cache_stale"}]}:{complete:true,items:[{id:"one",name:"Example"}]}),{status:200,headers:{"Content-Type":"application/json"}}));
 render(<LocaleProvider><CalendarStatus deviceId="6a36f507bf145264d1be1479864ad342"/></LocaleProvider>);expect(fetcher).not.toHaveBeenCalled();
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 expect(screen.getByRole("listitem").textContent).toContain("Example — Данные календаря ожидают обновления");expect(screen.queryByText(/недоступен/)).toBeNull();
 expect(fetcher.mock.calls.every(([,options])=>!options?.method||options.method==="GET")).toBe(true);
});
it("shows coverage reasons and known sync time while treating future timestamps as skew",async()=>{
 localStorage.setItem("homedash.ui.locale","ru");
 const futureDate=new Date(Date.now()+60_000);const future=futureDate.toISOString();
 vi.spyOn(globalThis,"fetch").mockImplementation(async(input)=>new Response(JSON.stringify(String(input).endsWith("calendar-status")?{date:"2026-10-31",complete:false,reason:"incomplete",sources:[{id:"one",state:"ready",stale:true,last_success_at:future,next_sync_at:future,coverage_complete:false,error_code:"permission",reason:"cache_stale",reasons:["cache_stale","coverage_incomplete","source_error"]}]}:{complete:true,items:[{id:"one",name:"Example"}]}),{status:200,headers:{"Content-Type":"application/json"}}));
 render(<LocaleProvider><CalendarStatus deviceId="11111111111111111111111111111111"/></LocaleProvider>);
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 const item=screen.getByRole("listitem");expect(item.textContent).toContain("Покрытие запрошенного периода неполное");expect(item.textContent).toContain("Последнее обновление");expect(item.textContent).toContain("Возможен сдвиг часов");expect(item.textContent).toContain("Нет прав на чтение календаря");expect(item.textContent).not.toContain("Источник сообщил об ошибке");expect(item.textContent).toContain(new Intl.DateTimeFormat("ru-RU",{dateStyle:"medium",timeStyle:"short"}).format(futureDate));
});
it("clears one device status and aborts its pending request when switching devices",async()=>{
 localStorage.setItem("homedash.ui.locale","ru");let firstSignal:AbortSignal|undefined;
 vi.spyOn(globalThis,"fetch").mockImplementation((input,init)=>{
  const url=String(input);
  if(url.includes("11111111111111111111111111111111")&&url.endsWith("calendar-status")){firstSignal=init?.signal as AbortSignal;return new Promise((_resolve,reject)=>firstSignal?.addEventListener("abort",()=>reject(new DOMException("Aborted","AbortError"))))}
  const isStatus=url.endsWith("calendar-status");const name=url.includes("22222222222222222222222222222222")?"Second device":"First device";
  return Promise.resolve(new Response(JSON.stringify(isStatus?{date:"2026-10-31",complete:true,reason:"complete",sources:[{id:name,state:"ready",stale:false,reason:"ready"}]}:{complete:true,items:[{id:name,name}]}),{status:200,headers:{"Content-Type":"application/json"}}));
 });
 const view=render(<LocaleProvider><CalendarStatus deviceId="11111111111111111111111111111111"/></LocaleProvider>);
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 expect(firstSignal?.aborted).toBe(false);
 view.rerender(<LocaleProvider><CalendarStatus deviceId="22222222222222222222222222222222"/></LocaleProvider>);
 expect(firstSignal?.aborted).toBe(true);expect(screen.queryByText(/First device/)).toBeNull();
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 expect(screen.getByRole("listitem").textContent).toContain("Second device — Данные актуальны");expect(screen.queryByText(/First device/)).toBeNull();
});
it("reports failed checks clearly and permits an explicit retry",async()=>{
 localStorage.setItem("homedash.ui.locale","ru");vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response('{}',{status:502,headers:{"Content-Type":"application/json"}}));
 render(<LocaleProvider><CalendarStatus deviceId="6a36f507bf145264d1be1479864ad342"/></LocaleProvider>);
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 expect(screen.getByRole("alert").textContent).toContain("Не удалось проверить календари");expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(false);
});
