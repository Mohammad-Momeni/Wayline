"""Optional desktop editor and session controller."""

import json
from pathlib import Path
from queue import Empty, Queue
from threading import Thread
import tkinter as tk
from tkinter import filedialog, messagebox, simpledialog, ttk
from uuid import uuid4


class WaylineWindow:
    def __init__(self, root, client):
        self.root = root
        self.client = client
        self.profiles = client.load()
        self.events = Queue()
        self.child = None
        self.closing = False
        self.fingerprint = ""
        self.once_fingerprint=""
        self.retry_certificate=False
        self.fields = {}
        self.hop_data = {"before": [], "after": []}
        self.hop_boxes = {}
        self.app_paths=[]
        self.xray_data=[]
        self.selected_xray=None
        self.theme_path = client.PROFILES.parent / "theme.txt"
        self.dark = not self.theme_path.exists() or self.theme_path.read_text().strip() != "light"
        root.title(f"Wayline {client.VERSION} · VPN")
        root.minsize(650, 480)
        root.geometry("920x720")
        icon_path = client.ENGINE.parent / "wayline.png"
        if icon_path.exists():
            self.icon = tk.PhotoImage(file=icon_path)
            root.iconphoto(True, self.icon)
        self.apply_theme()
        root.protocol("WM_DELETE_WINDOW", self.close)

        outer = ttk.Frame(root, padding=12)
        outer.pack(fill="both", expand=True)
        sidebar = ttk.Frame(outer, padding=(0, 0, 12, 0))
        sidebar.pack(side="left", fill="y")
        ttk.Label(sidebar, text="Profiles").pack(anchor="w")
        self.picker = tk.Listbox(sidebar, width=21, exportselection=False)
        self.picker.pack(fill="y", expand=True, pady=(4, 8))
        self.picker.bind("<<ListboxSelect>>", self.select)
        for label, action in (("New", self.new), ("Rename", self.rename_profile), ("Save", self.save), ("Import profile", self.import_profile),
                              ("Export profile", self.export_profile), ("Forget password", self.forget),
                              ("Dark / light mode", self.toggle_theme)):
            ttk.Button(sidebar, text=label, command=self.action(action)).pack(fill="x", pady=2)

        pane = ttk.Frame(outer)
        pane.pack(side="left", fill="both", expand=True)
        pane.columnconfigure(1, weight=1)
        tabs = ttk.Notebook(pane)
        self.tabs=tabs
        tabs.grid(row=0, columnspan=2, sticky="nsew")
        pane.rowconfigure(0, weight=1)
        settings = self.scroll_tab(tabs, "Connection")
        chains = self.scroll_tab(tabs, "Chain")
        xrays = self.scroll_tab(tabs, "Xrays")
        activity=self.scroll_tab(tabs,"Logs")
        xrays.columnconfigure(1,weight=1)
        activity.columnconfigure(0,weight=1)
        for tab in (settings, chains):
            tab.columnconfigure(1, weight=1)
        self.app_mode=tk.StringVar(value="all")
        self.app_summary=tk.StringVar()
        advanced=ttk.Frame(settings)
        advanced.columnconfigure(1,weight=1)
        self.advanced=advanced
        for row, (key, title) in enumerate((
            ("name", "Profile name"), ("gateway", "SSL-VPN gateway"), ("port", "Gateway port"),
            ("username", "Username"), ("password", "VPN password"), ("realm", "Realm"),
            ("certificateSha256", "IT-verified SHA-256"), ("listenPort", "Local proxy port"), ("dns", "VPN DNS override"),
        )):
            self.field(advanced if row>=5 else settings, row-5 if row>=5 else row, key, title, hidden=key == "password")
        ttk.Button(advanced,text="Manage trusted fingerprints",command=self.action(self.manage_certificates)).grid(row=5,columnspan=2,sticky="w",pady=8)
        ttk.Label(settings,text="Apps using the VPN").grid(row=14,column=0,sticky="w")
        ttk.Combobox(settings,textvariable=self.app_mode,values=("all","include","exclude"),state="readonly").grid(row=14,column=1,sticky="ew")
        ttk.Button(settings,text="Choose apps",command=self.action(self.choose_apps)).grid(row=15,column=0,sticky="w",pady=8)
        ttk.Label(settings,textvariable=self.app_summary).grid(row=15,column=1,sticky="w")
        ttk.Label(settings,text="VPN mode only. Shared system DNS still uses the VPN.",wraplength=450).grid(row=16,columnspan=2,sticky="w")
        self.advanced_visible=False
        ttk.Button(settings,text="Advanced options",command=self.toggle_advanced).grid(row=12,columnspan=2,sticky="w",pady=10)
        self.mode = tk.StringVar(value="proxy")
        ttk.Label(settings, text="Mode").grid(row=9, column=0, sticky="w", padx=(0, 8))
        ttk.Combobox(settings, textvariable=self.mode, values=("proxy", "vpn"), state="readonly").grid(row=9, column=1, sticky="ew")
        self.remember = tk.BooleanVar()
        ttk.Checkbutton(settings, text="Remember VPN password in desktop keyring", variable=self.remember).grid(row=10, columnspan=2, sticky="w", pady=6)
        self.reconnect=tk.BooleanVar()
        ttk.Checkbutton(settings,text="Auto reconnect after connection loss",variable=self.reconnect).grid(row=11,columnspan=2,sticky="w")
        for row, (side, label) in enumerate((("before", "Before VPN"), ("after", "After VPN"))):
            group = ttk.LabelFrame(chains, text=label, padding=6)
            group.grid(row=row, columnspan=3, sticky="ew", pady=5)
            self.hop_boxes[side] = group
        self.xray_box=ttk.LabelFrame(xrays,text="Saved Xray configurations",padding=6)
        self.xray_box.grid(row=0,columnspan=3,sticky="ew",pady=6)
        for row, (key, label) in enumerate((
            ("xrayName","Configuration name"),("xrayConfig", "Xray JSON path"), ("xrayExecutable", "Xray executable path"), ("xrayOutboundTag", "Xray outbound tag"),
        ), start=1):
            self.field(xrays, row, key, label)
        for row, key in ((2, "xrayConfig"), (3, "xrayExecutable")):
            ttk.Button(xrays, text="Browse…", command=lambda key=key: self.browse(key)).grid(row=row, column=2, padx=(6, 0))
        ttk.Button(xrays,text="Edit configuration settings",command=self.action(self.edit_xray_settings)).grid(row=5,columnspan=3,sticky="ew",pady=6)
        import_bar = ttk.Frame(xrays)
        import_bar.grid(row=6, columnspan=3, sticky="ew", pady=8)
        ttk.Button(import_bar,text="Import file",command=self.action(self.import_xray_file)).pack(side="left",padx=4)
        ttk.Button(import_bar, text="Paste Xray", command=self.action(self.paste_xray)).pack(side="left")
        ttk.Button(import_bar, text="Xray share link…", command=self.action(self.enter_xray)).pack(side="left", padx=6)
        ttk.Label(chains, text="Tick the hops to use. Enter a username in a proxy URL to request a password at Connect.",
                  wraplength=450, justify="left").grid(row=8, columnspan=3, sticky="w", pady=10)

        bar = ttk.Frame(pane)
        bar.grid(row=1, columnspan=2, sticky="ew", pady=8)
        self.connect_button = ttk.Button(bar, text="Connect", command=self.action(self.connect))
        self.connect_button.pack(side="left")
        self.stop_button = ttk.Button(bar, text="Disconnect", command=self.stop, state="disabled")
        self.stop_button.pack(side="left", padx=6)
        ttk.Button(bar, text="Copy gateway fingerprint", command=self.copy_certificate).pack(side="right")
        self.status = tk.StringVar(value="Disconnected")
        ttk.Label(pane, textvariable=self.status).grid(row=2, columnspan=2, sticky="w")
        self.log = tk.Text(activity, height=22, width=55, state="disabled", wrap="word")
        self.log.grid(row=1,column=0,sticky="nsew",pady=(8,0))
        activity.rowconfigure(1,weight=1)
        log_actions=ttk.Frame(activity)
        log_actions.grid(row=0,column=0,sticky="ew")
        ttk.Button(log_actions,text="Copy logs",command=self.copy_logs).pack(side="left")
        ttk.Button(log_actions,text="Clear logs",command=self.clear_logs).pack(side="left",padx=6)
        self.populate(next(iter(self.profiles), None))
        self.root.after(100, self.poll)

    def choose_apps(self):
        dialog=tk.Toplevel(self.root);dialog.title("Choose VPN apps");dialog.geometry("640x520");dialog.transient(self.root)
        inventory=set(self.app_paths)
        for path in Path("/proc").glob("[0-9]*/exe"):
            try:inventory.add(str(path.resolve(strict=True)))
            except OSError:pass
        selected=set(self.app_paths);query=tk.StringVar();ttk.Entry(dialog,textvariable=query).pack(fill="x",padx=16,pady=12)
        count=tk.StringVar();ttk.Label(dialog,textvariable=count).pack(anchor="w",padx=16)
        canvas=tk.Canvas(dialog,highlightthickness=0);canvas.pack(fill="both",expand=True,padx=16,pady=8);rows=ttk.Frame(canvas);window=canvas.create_window((0,0),window=rows,anchor="nw");canvas.bind("<Configure>",lambda e:canvas.itemconfigure(window,width=e.width));rows.bind("<Configure>",lambda e:canvas.configure(scrollregion=canvas.bbox("all")))
        scroll=ttk.Scrollbar(dialog,command=canvas.yview);scroll.pack(side="right",fill="y");canvas.configure(yscrollcommand=scroll.set)
        def render(*_):
            for widget in rows.winfo_children():widget.destroy()
            for path in sorted(inventory,key=lambda v:Path(v).name.lower()):
                if query.get().lower() not in path.lower():continue
                checked=tk.BooleanVar(value=path in selected)
                def change(path=path,checked=checked):
                    if checked.get():selected.add(path)
                    else:selected.discard(path)
                    count.set(f"{len(selected)} apps selected")
                ttk.Checkbutton(rows,text=Path(path).name+"\n"+path,variable=checked,command=change).pack(fill="x",anchor="w",pady=5)
            count.set(f"{len(selected)} apps selected")
        query.trace_add("write",render)
        def browse():
            for value in filedialog.askopenfilenames(parent=dialog,title="Choose executables"):
                path=str(Path(value).resolve());inventory.add(path);selected.add(path)
            render()
        def apply():self.app_paths=sorted(selected);self.app_summary.set(f"{len(selected)} apps selected");dialog.destroy()
        bar=ttk.Frame(dialog);bar.pack(fill="x",padx=16,pady=12);ttk.Button(bar,text="Browse executables",command=browse).pack(side="left");ttk.Button(bar,text="Apply selection",command=apply).pack(side="right");render()

    def toggle_advanced(self):
        self.advanced_visible=not self.advanced_visible
        if self.advanced_visible:self.advanced.grid(row=13,columnspan=2,sticky="ew")
        else:self.advanced.grid_remove()

    def rename_profile(self):
        if self.child:return
        value=simpledialog.askstring("Rename VPN profile","Profile name",initialvalue=self.fields["name"].get(),parent=self.root)
        if value and value.strip():
            self.fields["name"].set(value.strip())
            self.save()

    def clear_logs(self):
        self.log.configure(state="normal");self.log.delete("1.0",tk.END);self.log.configure(state="disabled")

    def copy_logs(self):
        self.root.clipboard_clear();self.root.clipboard_append(self.log.get("1.0",tk.END))

    def open_xray(self,entry):
        self.select_xray(entry)
        self.edit_xray_settings()

    def edit_xray_settings(self):
        self.commit_xray()
        entry=self.selected_xray
        if entry is None:raise ValueError("Select a saved Xray configuration first.")
        config=json.loads(Path(entry["configFile"]).read_text(encoding="utf-8-sig"))
        out=next((o for o in config["outbounds"] if o.get("tag")==entry["outboundTag"]),None) if entry.get("outboundTag") else next((o for o in config["outbounds"] if o.get("protocol") not in {"freedom","blackhole"}),None)
        if out is None:raise ValueError("Select a matching outbound tag.")
        settings=out["settings"];server=(settings.get("vnext") or settings.get("servers"))[0];account=(server.get("users") or [server])[0]
        dialog=tk.Toplevel(self.root);dialog.title("Xray settings · "+entry["name"]);dialog.geometry("640x620");dialog.transient(self.root)
        tabs=ttk.Notebook(dialog);tabs.pack(fill="both",expand=True,padx=16,pady=12)
        form=ttk.Frame(tabs,padding=12);raw=ttk.Frame(tabs);tabs.add(form,text="Connection");tabs.add(raw,text="JSON settings");form.columnconfigure(1,weight=1)
        fields={}
        for row,(key,label,value) in enumerate([("name","Configuration name",entry["name"]),("address","Server",server["address"]),("port","Port",str(server["port"])),("credential","User ID / password",account.get("id",account.get("password",""))),("tag","Outbound tag",out.get("tag",""))]):
            ttk.Label(form,text=label).grid(row=row,column=0,sticky="w",pady=8,padx=(0,12));fields[key]=tk.StringVar(value=value);ttk.Entry(form,textvariable=fields[key],show="*" if key=="credential" else "").grid(row=row,column=1,sticky="ew")
        ttk.Label(form,text="Use JSON settings for transport, TLS, Reality and all other options.",wraplength=500).grid(row=6,columnspan=2,sticky="w",pady=20)
        text=tk.Text(raw,wrap="none",font=("Consolas",11));text.pack(fill="both",expand=True);text.insert("1.0",json.dumps(config,indent=2))
        def save():
            updated=json.loads(text.get("1.0",tk.END)) if tabs.index(tabs.select())==1 else config
            if tabs.index(tabs.select())==0:
                number=int(fields["port"].get())
                if not fields["address"].get().strip() or not 1<=number<=65535:raise ValueError("Enter a server and valid port.")
                server.update(address=fields["address"].get().strip(),port=number)
                key="id" if "id" in account else "password"
                if key in account or fields["credential"].get():account[key]=fields["credential"].get()
                out["tag"]=fields["tag"].get().strip()
            if not isinstance(updated.get("outbounds"),list) or not updated["outbounds"]:raise ValueError("Configuration needs outbounds.")
            name=fields["name"].get().strip()
            if not name:raise ValueError("Enter a name.")
            updated["remarks"]=name
            path=self.client.import_xray(json.dumps(updated))
            entry.update(name=name,configFile=path,outboundTag=fields["tag"].get().strip())
            self.selected_xray=None;self.select_xray(entry);self.save();self.render_xrays();self.render_hops("before");self.render_hops("after");dialog.destroy()
        ttk.Button(dialog,text="Save configuration",command=self.action(save)).pack(fill="x",padx=16,pady=(0,16))

    def manage_certificates(self):
        trusted=self.profiles.get(self.selected,{}).get("trustedCertificates",[])
        dialog=tk.Toplevel(self.root);dialog.title("Trusted fingerprints");dialog.transient(self.root)
        box=tk.Listbox(dialog,width=70,height=8);box.pack(fill="both",expand=True,padx=16,pady=16)
        for value in trusted:box.insert(tk.END,value)
        def remove():
            if box.curselection():
                index=box.curselection()[0];trusted.pop(index);box.delete(index);self.client.save(self.profiles)
        ttk.Button(dialog,text="Remove selected",command=remove).pack(pady=(0,16))

    def certificate_decision(self,fingerprint):
        dialog=tk.Toplevel(self.root);dialog.title("Gateway certificate");dialog.transient(self.root);dialog.grab_set()
        panel=ttk.Frame(dialog,padding=22);panel.pack(fill="both",expand=True)
        ttk.Label(panel,text="The gateway certificate could not be verified. Proceed only if you recognize or independently verified this SHA-256 fingerprint.",wraplength=490).pack(anchor="w")
        value=ttk.Entry(panel,width=68);value.insert(0,fingerprint);value.configure(state="readonly");value.pack(fill="x",pady=16)
        remember=tk.BooleanVar();ttk.Checkbutton(panel,text="Remember for this VPN profile",variable=remember).pack(anchor="w")
        def proceed():
            if remember.get():
                profile=self.profiles[self.selected];trusted=profile.setdefault("trustedCertificates",[])
                if fingerprint not in trusted:
                    if len(trusted)>=31:raise ValueError("Remove an old fingerprint first.")
                    trusted.append(fingerprint)
                self.client.save(self.profiles)
            else:self.once_fingerprint=fingerprint
            self.retry_certificate=True
            if self.child is None:self.retry_certificate=False;self.root.after_idle(self.action(self.connect))
            dialog.destroy()
        actions=ttk.Frame(panel);actions.pack(fill="x",pady=(18,0));ttk.Button(actions,text="Cancel",command=dialog.destroy).pack(side="left");ttk.Button(actions,text="Proceed",command=self.action(proceed)).pack(side="right")

    def scroll_tab(self, tabs, title):
        container = ttk.Frame(tabs)
        tabs.add(container, text=title)
        canvas = tk.Canvas(container, highlightthickness=0, bg=self.palette["bg"])
        scrollbar = ttk.Scrollbar(container, orient="vertical", command=canvas.yview)
        canvas.configure(yscrollcommand=scrollbar.set)
        scrollbar.pack(side="right", fill="y")
        canvas.pack(side="left", fill="both", expand=True)
        inner = ttk.Frame(canvas, padding=10)
        window = canvas.create_window((0, 0), window=inner, anchor="nw")
        inner.bind("<Configure>", lambda _event: canvas.configure(scrollregion=canvas.bbox("all")))
        canvas.bind("<Configure>", lambda event: canvas.itemconfigure(window, width=event.width))
        canvas.bind("<MouseWheel>", lambda event: canvas.yview_scroll(-int(event.delta / 120), "units"))
        canvas.bind("<Button-4>", lambda _event: canvas.yview_scroll(-1, "units"))
        canvas.bind("<Button-5>", lambda _event: canvas.yview_scroll(1, "units"))
        return inner

    def apply_theme(self):
        self.palette = ({"bg": "#101823", "panel": "#1b2838", "fg": "#e4edf7", "accent": "#53d9c3"}
                        if self.dark else {"bg": "#f1f5f8", "panel": "#ffffff", "fg": "#1c293e", "accent": "#116d66"})
        p = self.palette
        style = ttk.Style(self.root)
        style.theme_use("clam")
        style.configure(".", background=p["bg"], foreground=p["fg"], fieldbackground=p["panel"])
        style.configure("TButton", background=p["panel"], padding=(9, 5))
        style.map("TButton", background=[("active", p["accent"])], foreground=[("active", "#101823")])
        style.configure("TNotebook.Tab", background=p["panel"], padding=(10, 6))
        style.map("TNotebook.Tab", background=[("selected", p["accent"])], foreground=[("selected", "#101823")])
        style.configure("TEntry", fieldbackground=p["panel"], insertcolor=p["fg"])
        self.root.option_add("*Background", p["bg"])
        self.root.option_add("*Foreground", p["fg"])
        self.root.option_add("*selectBackground", p["accent"])
        self.root.option_add("*selectForeground", "#101823")
        self.root.option_add("*insertBackground", p["fg"])
        self.root.configure(bg=p["bg"])
        def color(widget):
            if isinstance(widget, (tk.Text, tk.Listbox)):
                widget.configure(bg=p["panel"], fg=p["fg"], selectbackground=p["accent"],
                                 selectforeground="#101823", highlightbackground=p["bg"])
            elif isinstance(widget, tk.Canvas):
                widget.configure(bg=p["bg"])
            for child in widget.winfo_children():
                color(child)
        color(self.root)

    def toggle_theme(self):
        self.dark = not self.dark
        self.theme_path.parent.mkdir(parents=True, exist_ok=True)
        self.theme_path.write_text("dark" if self.dark else "light")
        self.apply_theme()

    def render_hops(self, side):
        group = self.hop_boxes[side]
        for widget in group.winfo_children():
            widget.destroy()
        for index, hop in enumerate(self.hop_data[side]):
            row = ttk.Frame(group)
            row.pack(fill="x")
            enabled = tk.BooleanVar(value=hop.get("enabled", True))
            def toggle(hop=hop, enabled=enabled):
                hop["enabled"] = enabled.get()
                self.render_xrays()
            ttk.Checkbutton(row, variable=enabled, command=toggle).pack(side="left")
            saved=next((entry for entry in self.xray_data if entry["id"]==hop.get("xrayId")),None)
            label = "Xray · "+saved["name"] if saved else self.client.format_hop(hop).removeprefix("!")
            ttk.Button(row, text=label, command=self.action(lambda side=side, index=index: self.edit_hop(side, index))).pack(side="left", fill="x", expand=True)
            ttk.Button(row, text="↑", width=2, command=lambda side=side, index=index: self.move_hop(side, index)).pack(side="left")
            ttk.Button(row, text="×", width=2, command=lambda side=side, index=index: self.remove_hop(side, index)).pack(side="left")
        ttk.Button(group, text="Add hop", command=self.action(lambda: self.edit_hop(side))).pack(anchor="w", pady=4)

    def edit_hop(self, side, index=None):
        if index is not None and self.hop_data[side][index].get("type")=="xray":
            entry=next((entry for entry in self.xray_data if entry["id"]==self.hop_data[side][index]["xrayId"]),None)
            if entry:self.select_xray(entry)
            return
        existing = "" if index is None else self.client.format_hop(self.hop_data[side][index])
        value = simpledialog.askstring("Proxy hop", "Proxy URL, for example socks5://127.0.0.1:10808",
                                       initialvalue=existing, parent=self.root)
        if value:
            hop = self.client.parse_hop(value.strip())
            if index is None:
                self.hop_data[side].append(hop)
            else:
                self.hop_data[side][index] = hop
            self.render_hops(side)

    def move_hop(self, side, index):
        if index > 0:
            chain = self.hop_data[side]
            chain[index - 1], chain[index] = chain[index], chain[index - 1]
            self.render_hops(side)

    def remove_hop(self, side, index):
        self.hop_data[side].pop(index)
        self.render_hops(side)
        self.render_xrays()

    def paste_xray(self):
        self.add_xray(self.root.clipboard_get())

    def enter_xray(self):
        text = simpledialog.askstring("Xray share link", "Paste a vmess://, vless://, trojan:// or ss:// link.", parent=self.root)
        if text:
            self.add_xray(text)

    def import_xray_file(self):
        path=filedialog.askopenfilename(parent=self.root,filetypes=[("Xray JSON","*.json"),("All files","*")])
        if path:
            with Path(path).open("r",encoding="utf-8-sig") as source:text=source.read(self.client.SIZES["profile"]+1)
            self.add_xray(text)

    def add_xray(self,text):
        self.commit_xray()
        path=self.client.import_xray(text)
        entry={"id":uuid4().hex,"name":json.loads(Path(path).read_text()).get("remarks") or f"Xray {len(self.xray_data)+1}","configFile":path,"executable":self.fields["xrayExecutable"].get(),"outboundTag":""}
        self.xray_data.append(entry)
        self.hop_data["after"].append({"type":"xray","xrayId":entry["id"],"enabled":True})
        self.select_xray(entry)
        self.render_xrays()
        self.render_hops("after")

    def commit_xray(self):
        if self.selected_xray is not None:
            self.selected_xray.update(name=self.fields["xrayName"].get().strip() or "Xray",
                configFile=self.fields["xrayConfig"].get().strip(),executable=self.fields["xrayExecutable"].get().strip(),
                outboundTag=self.fields["xrayOutboundTag"].get().strip())

    def select_xray(self,entry):
        self.commit_xray()
        self.selected_xray=entry
        for field,key in [("xrayName","name"),("xrayConfig","configFile"),("xrayExecutable","executable"),("xrayOutboundTag","outboundTag")]:
            self.fields[field].set(entry.get(key,""))

    def render_xrays(self):
        for widget in self.xray_box.winfo_children():widget.destroy()
        for entry in self.xray_data:
            row=ttk.Frame(self.xray_box)
            row.pack(fill="x")
            enabled=tk.BooleanVar(value=any(h.get("xrayId")==entry["id"] and h.get("enabled",True) for side in ("before","after") for h in self.hop_data[side]))
            def toggle(entry=entry,enabled=enabled):
                references=[h for side in ("before","after") for h in self.hop_data[side] if h.get("xrayId")==entry["id"]]
                for hop in references:hop["enabled"]=enabled.get()
                if enabled.get() and not references:self.hop_data["after"].append({"type":"xray","xrayId":entry["id"],"enabled":True})
                for side in ("before","after"):self.render_hops(side)
            ttk.Checkbutton(row,variable=enabled,command=toggle).pack(side="left")
            ttk.Button(row,text=entry["name"]+" · Settings",command=lambda entry=entry:self.open_xray(entry)).pack(side="left",fill="x",expand=True)
            for side,label in [("before","Before"),("after","After")]:
                ttk.Button(row,text=label,command=lambda entry=entry,side=side:self.add_xray_hop(entry,side)).pack(side="left")
            ttk.Button(row,text="×",width=2,command=lambda entry=entry:self.delete_xray(entry)).pack(side="left")
        if not self.xray_data:ttk.Label(self.xray_box,text="Import a JSON file or paste a share link to add an Xray.").pack(anchor="w")

    def add_xray_hop(self,entry,side):
        self.hop_data[side].append({"type":"xray","xrayId":entry["id"],"enabled":True})
        self.render_hops(side)
        self.render_xrays()

    def delete_xray(self,entry):
        if not messagebox.askyesno("Delete saved Xray","Remove this configuration and its chain references?",parent=self.root):return
        for side in ("before","after"):
            self.hop_data[side]=[hop for hop in self.hop_data[side] if hop.get("xrayId")!=entry["id"]]
            self.render_hops(side)
        self.xray_data.remove(entry)
        if self.selected_xray is entry:
            self.selected_xray=None
            for field in ("xrayName","xrayConfig","xrayExecutable","xrayOutboundTag"):self.fields[field].set("")
        self.render_xrays()

    def field(self, pane, row, key, label, hidden=False):
        self.fields[key] = tk.StringVar()
        ttk.Label(pane, text=label).grid(row=row, column=0, sticky="w", padx=(0, 8))
        ttk.Entry(pane, textvariable=self.fields[key], show="*" if hidden else "").grid(row=row, column=1, sticky="ew", pady=3)

    def action(self, function):
        def wrapped():
            try:
                function()
            except Exception as error:
                messagebox.showerror("Wayline", str(error), parent=self.root)
        return wrapped

    def populate(self, name):
        self.picker.delete(0, tk.END)
        names = list(self.profiles)
        for item in names:
            self.picker.insert(tk.END, item)
        if name in names:
            self.picker.selection_set(names.index(name))
        self.fill(name)

    def select(self, _event):
        if self.picker.curselection():
            self.fill(self.picker.get(self.picker.curselection()[0]))

    def fill(self, name):
        self.selected = name
        profile = dict(self.client.FIELDS, **self.profiles.get(name, {}))
        self.selected_xray=None
        self.xray_data=[entry.copy() for entry in profile.get("xrays",[])]
        for key, field in self.fields.items():
            value = profile.get(key, "")
            if key in ("before", "after"):
                value = " ".join(map(self.client.format_hop, value))
            elif key == "dns":
                value = " ".join(value)
            elif key == "name":
                value = name or "VPN"
            field.set(value)
        self.mode.set(profile["mode"])
        self.reconnect.set(profile.get("autoReconnect",False))
        self.app_mode.set(profile.get("appFilterMode","all"));self.app_paths=list(profile.get("appFilterApps",[]));self.app_summary.set(f"{len(self.app_paths)} apps selected")
        for side in ("before", "after"):
            self.hop_data[side] = [hop.copy() for hop in profile[side]]
            self.render_hops(side)
        self.render_xrays()
        self.remember.set(False)

    def new(self):
        self.picker.selection_clear(0, tk.END)
        self.fill(None)

    def browse(self, key):
        filename = filedialog.askopenfilename(parent=self.root)
        if filename:
            self.fields[key].set(filename)

    def values(self):
        self.commit_xray()
        raw = {key: field.get().strip() for key, field in self.fields.items() if key != "password"}
        raw["mode"] = self.mode.get()
        raw["autoReconnect"]=self.reconnect.get()
        raw["appFilterMode"]=self.app_mode.get();raw["appFilterApps"]=list(self.app_paths)
        raw["dns"] = raw["dns"].replace(",", " ").split()
        for side in ("before", "after"):
            raw[side] = self.hop_data[side]
        name = raw["name"]
        if not name or name in self.profiles and name != self.selected:
            raise ValueError("Choose a unique profile name.")
        profile = self.client.normalize(raw,allow_draft=True)
        profile["xrays"]=[entry.copy() for entry in self.xray_data]
        profile["trustedCertificates"]=self.profiles.get(self.selected,{}).get("trustedCertificates",[]) if (profile["gateway"],profile["port"])==(self.profiles.get(self.selected,{}).get("gateway"),self.profiles.get(self.selected,{}).get("port")) else []
        profile["id"] = self.profiles.get(self.selected, {}).get("id", uuid4().hex)
        return name, profile

    def save(self):
        name, profile = self.values()
        updated = self.profiles.copy()
        updated.pop(self.selected, None)
        updated[name] = profile
        self.client.save(updated)
        self.profiles = updated
        password, remember = self.fields["password"].get(), self.remember.get()
        self.populate(name)
        self.fields["password"].set(password)
        self.remember.set(remember)
        return profile

    def import_profile(self):
        path = filedialog.askopenfilename(parent=self.root, filetypes=[("Wayline JSON", "*.json"), ("All files", "*")])
        if path:
            name = simpledialog.askstring("Import profile", "Profile name (blank to use the file's name)", parent=self.root)
            if name is not None:
                self.client.import_profile(self.profiles, path, name.strip())
                self.populate(next(reversed(self.profiles)))

    def export_profile(self):
        name, profile = self.values()
        path = filedialog.asksaveasfilename(parent=self.root, defaultextension=".json", initialfile="wayline-profile.json")
        if path:
            data = dict(self.client.normalize(profile,portable=True), name=name)
            Path(path).write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")

    def forget(self):
        if self.selected:
            self.client.secret_tool("clear", self.profiles[self.selected]["id"])
            self.fields["password"].set("")
            self.remember.set(False)

    def connect(self):
        if self.child:
            return
        profile = self.save()
        session = self.client.session_config(profile)
        if self.once_fingerprint:
            session["certificateSha256"]=self.once_fingerprint
            self.once_fingerprint=""
        password = self.fields["password"].get() or self.client.secret_tool("lookup", profile["id"])
        if not password:
            password = simpledialog.askstring("VPN password", "Enter your VPN password.", show="*", parent=self.root)
        if not password:
            return
        session["password"] = password
        for side in ("before", "after"):
            for hop in session[side]:
                if hop.get("username"):
                    value = simpledialog.askstring("Proxy password", f"{side.title()} proxy at {hop['host']}",
                                                   show="*", parent=self.root)
                    if value is None:
                        return
                    hop["password"] = value
        if self.remember.get():
            self.client.secret_tool("store", profile["id"], password)
        self.fingerprint = ""
        self.child = self.client.start_session(session, gui=True)
        self.status.set("Connecting…")
        self.connect_button.configure(state="disabled")
        self.stop_button.configure(state="normal")
        Thread(target=self.read_events, args=(self.child,), daemon=True).start()

    def read_events(self, child):
        try:
            with child.stdout:
                for line in child.stdout:
                    self.events.put(json.loads(line))
        except (OSError, ValueError) as error:
            self.events.put({"type": "error", "message": f"Could not read the engine output. {error}"})
        finally:
            self.client.close_session_input(child)
            self.events.put({"type": "exit", "message": f"Session ended ({child.wait()})."})

    def stop(self):
        if self.child:
            self.status.set("Disconnecting…")
            self.stop_button.configure(state="disabled")
            self.client.close_session_input(self.child)

    def close(self):
        if self.child:
            self.closing = True
            self.stop()
        else:
            self.root.destroy()

    def copy_certificate(self):
        if self.fingerprint:
            self.root.clipboard_clear()
            self.root.clipboard_append(self.fingerprint)

    def poll(self):
        try:
            while True:
                event = self.events.get_nowait()
                kind = event["type"]
                if kind == "stats":
                    continue
                message = event["message"]
                if kind == "exit":
                    self.child = None
                    if self.closing:
                        self.root.destroy()
                        return
                    self.status.set("Disconnected")
                    self.connect_button.configure(state="normal")
                    self.stop_button.configure(state="disabled")
                    if self.retry_certificate:
                        self.retry_certificate=False
                        self.root.after_idle(self.action(self.connect))
                elif kind == "status":
                    self.status.set(message)
                elif kind == "certificate":
                    self.fingerprint = event["sha256"]
                    message += "\nSHA-256 " + self.fingerprint
                    self.certificate_decision(self.fingerprint)
                self.log.configure(state="normal")
                self.log.insert(tk.END, message + "\n")
                self.log.see(tk.END)
                self.log.configure(state="disabled")
        except Empty:
            self.root.after(100, self.poll)


def run_gui(client):
    try:
        root = tk.Tk()
    except tk.TclError as error:
        raise ValueError("Could not open a display. Start Wayline without --gui for the terminal client.") from error
    WaylineWindow(root, client)
    root.mainloop()
    return 0
