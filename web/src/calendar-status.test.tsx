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
 expect(screen.getByText(/Example — Данные календаря ожидают обновления/)).toBeTruthy();expect(screen.queryByText(/недоступен/)).toBeNull();
 expect(fetcher.mock.calls.every(([,options])=>!options?.method||options.method==="GET")).toBe(true);
});
it("reports failed checks clearly and permits an explicit retry",async()=>{
 localStorage.setItem("homedash.ui.locale","ru");vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response('{}',{status:502,headers:{"Content-Type":"application/json"}}));
 render(<LocaleProvider><CalendarStatus deviceId="6a36f507bf145264d1be1479864ad342"/></LocaleProvider>);
 await act(async()=>fireEvent.click(screen.getByRole("button",{name:"Проверить календари"})));
 expect(screen.getByRole("alert").textContent).toContain("Не удалось проверить календари");expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(false);
});
