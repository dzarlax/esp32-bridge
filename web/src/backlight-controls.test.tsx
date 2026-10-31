import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BacklightControls } from "./backlight-controls";
import { LocaleProvider } from "./i18n";
import type { Command, Device, Envelope, Settings } from "./types";
const current = Date.parse("2026-10-31T16:00:00Z");
function fixture() {
  const draft = { screen: { name:"Home",city:"Belgrade",timezone:"Europe/Belgrade",clock_format:"24h",idle_action:"photos",night_enabled:false,night_start:"23:00",night_end:"07:00",auto_update_check:true,auto_install:false } } as Settings;
  const device: Device={connected:true,last_seen:new Date(current).toISOString(),applied_revision:6,firmware:"1.0.36",ip:"",ssid:"",uptime_seconds:1,time_synced:true,capabilities:["backlight_brightness","backlight_preview","backlight_adjust","backlight_begin","backlight_restore"],error:"",command:null};
  const envelope={device,settings:draft,revision:6,capabilities:{brightness:true}} as Envelope;
  const runAction=vi.fn(async (action:string,percent?:number):Promise<Command>=>({id:"preview-1",action,percent,state:"queued",created_at:new Date(current).toISOString(),expires_at:new Date(current+120000).toISOString()}));
  return {draft,edit:vi.fn(),envelope,runAction,busy:false,pendingCommand:false,deviceObservationFresh:true};
}
function view(props:ReturnType<typeof fixture>) {return <LocaleProvider><BacklightControls {...props}/></LocaleProvider>;}
function ack(props:ReturnType<typeof fixture>,percent=36) {
  props.envelope={...props.envelope,device:{...props.envelope.device,command:{id:"preview-1",action:"backlight_begin",percent,state:"succeeded",created_at:new Date(current).toISOString(),expires_at:new Date(current+120000).toISOString()},backlight_report:{version:1,phase:"preview",last_command:70,output_known:true,test_command:70,return_command:50,sample_uptime_ms:1000,restore_deadline_uptime_ms:21000,error:""}}};
}
const advance=async(ms:number)=>{await act(async()=>{await vi.advanceTimersByTimeAsync(ms);});};
beforeEach(()=>{vi.useFakeTimers();vi.setSystemTime(current);localStorage.setItem("homedash.ui.locale","ru");});
afterEach(()=>{cleanup();vi.useRealTimers();localStorage.clear();});
it("never previews on load; coalesces slider movements and requires matching ACK before choosing",async()=>{
 const p=fixture();const {rerender}=render(view(p));await advance(1000);expect(p.runAction).not.toHaveBeenCalled();
 const slider=screen.getByLabelText("Подбор яркости");
 for(const value of [70,55,36])fireEvent.change(slider,{target:{value:String(value)}});
 await advance(499);expect(p.runAction).not.toHaveBeenCalled();await advance(1);
 expect(p.runAction).toHaveBeenCalledTimes(1);expect(p.runAction).toHaveBeenCalledWith("backlight_begin",36);
 expect((screen.getByRole("button",{name:"Использовать ночью"}) as HTMLButtonElement).disabled).toBe(true);
 ack(p);rerender(view(p));
 fireEvent.click(screen.getByRole("button",{name:"Использовать ночью"}));
 expect(p.edit).toHaveBeenCalledWith(expect.objectContaining({screen:expect.objectContaining({night_backlight_percent:36})}));
 expect(p.edit.mock.calls[0][0].screen.backlight_percent).toBeUndefined();
 expect(p.runAction).toHaveBeenLastCalledWith("backlight_restore");
});
it.each(["different-command","different-percent","stale","offline","returned","expired","failed"])("does not choose an unconfirmed candidate: %s",async reason=>{
 const p=fixture();const {rerender}=render(view(p));fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);ack(p);
 if(reason==="different-command")p.envelope.device.command!.id="other";
 if(reason==="different-percent")p.envelope.device.command!.percent=40;
 if(reason==="stale")p.deviceObservationFresh=false;
 if(reason==="offline")p.envelope.device.connected=false;
 if(reason==="returned"){p.envelope.device.backlight_report!.phase="restored";p.envelope.device.backlight_report!.last_command=50;}
 if(reason==="failed")p.envelope.device.command!.state="failed";
 if(reason==="expired")vi.setSystemTime(current+21000);
 rerender(view(p));expect((screen.getByRole("button",{name:"Использовать днём"}) as HTMLButtonElement).disabled).toBe(true);expect(p.edit).not.toHaveBeenCalled();
});
it("invalidates the previous ACK as soon as the slider moves again",async()=>{
 const p=fixture();const {rerender}=render(view(p));fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);ack(p);rerender(view(p));
 expect((screen.getByRole("button",{name:"Использовать днём"}) as HTMLButtonElement).disabled).toBe(false);
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"80"}});
 expect((screen.getByRole("button",{name:"Использовать днём"}) as HTMLButtonElement).disabled).toBe(true);
});
it("does not replay a transport failure or continue after unmount",async()=>{
 const p=fixture();p.runAction.mockRejectedValueOnce(new Error("lost"));const {unmount}=render(view(p));
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);await advance(10000);expect(p.runAction).toHaveBeenCalledTimes(1);
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"40"}});unmount();await advance(1000);expect(p.runAction).toHaveBeenCalledTimes(1);
});
it("keeps legacy preview manual and does not enable dimming without two chosen levels",async()=>{
 const p=fixture();p.envelope.device.capabilities=p.envelope.device.capabilities.filter(x=>x!=="backlight_adjust");render(view(p));
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(1000);expect(p.runAction).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Проверить на 20 секунд"}));await advance(500);expect(p.runAction).toHaveBeenCalledWith("backlight_preview",36);
 expect((screen.getByRole("option",{name:"Уменьшить яркость"}) as HTMLOptionElement).disabled).toBe(true);
});
it("serializes in-flight requests and sends only the latest waiting level",async()=>{
 const p=fixture();let resolve!:(c:Command)=>void;
 p.runAction.mockImplementationOnce(()=>new Promise<Command>(r=>{resolve=r;}));
 const {rerender}=render(view(p));fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"60"}});fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"80"}});await advance(1000);expect(p.runAction).toHaveBeenCalledTimes(1);
 p.pendingCommand=true;p.envelope.device.command={id:"preview-1",action:"backlight_begin",percent:36,state:"queued",created_at:new Date(current).toISOString(),expires_at:new Date(current+120000).toISOString()};rerender(view(p));
 await act(async()=>{resolve(p.envelope.device.command!);});await advance(1000);expect(p.runAction).toHaveBeenCalledTimes(1);
 ack(p);p.pendingCommand=false;rerender(view(p));await advance(500);expect(p.runAction).toHaveBeenLastCalledWith("backlight_adjust",80);
});
it("drops a queued continuation after expiry and starts again only on explicit retry",async()=>{
 const p=fixture();const {rerender}=render(view(p));fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);ack(p);rerender(view(p));
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"80"}});
 p.envelope={...p.envelope,device:{...p.envelope.device,backlight_report:{...p.envelope.device.backlight_report!,phase:"restored",last_command:50}}};rerender(view(p));await advance(1000);expect(p.runAction).toHaveBeenCalledTimes(1);
 fireEvent.click(screen.getByRole("button",{name:"Проверить на 20 секунд"}));await advance(500);expect(p.runAction).toHaveBeenLastCalledWith("backlight_begin",80);
});
it("does not apply an unsent level automatically after offline recovery",async()=>{
 const p=fixture();const {rerender}=render(view(p));fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});
 p.envelope={...p.envelope,device:{...p.envelope.device,connected:false}};rerender(view(p));await advance(500);
 p.envelope={...p.envelope,device:{...p.envelope.device,connected:true}};rerender(view(p));await advance(1000);expect(p.runAction).not.toHaveBeenCalled();
});

it("shows a saved-draft hint after its own restore is acknowledged",async()=>{
 const p=fixture();const {rerender}=render(view(p));
 fireEvent.change(screen.getByLabelText("Подбор яркости"),{target:{value:"36"}});await advance(500);ack(p);rerender(view(p));
 p.runAction.mockResolvedValueOnce({...p.envelope.device.command!,id:"restore-1",action:"backlight_restore",state:"queued"});
 fireEvent.click(screen.getByRole("button",{name:"Использовать ночью"}));await advance(0);
 p.envelope={...p.envelope,device:{...p.envelope.device,command:{...p.envelope.device.command!,id:"restore-1",action:"backlight_restore",state:"succeeded"},backlight_report:{...p.envelope.device.backlight_report!,phase:"restored",last_command:50}}};rerender(view(p));
 expect(screen.getByText("Уровень выбран в черновике. Можно сохранить изменения.")).toBeTruthy();
});
