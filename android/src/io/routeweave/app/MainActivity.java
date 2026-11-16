package io.routeweave.app;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.text.InputType;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;
import android.widget.ImageView;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.concurrent.FutureTask;
import java.util.concurrent.TimeUnit;
import org.json.JSONArray;
import org.json.JSONObject;

public final class MainActivity extends Activity {
    private ProfileStore store;
    private JSONArray profiles;
    private JSONObject current;
    private int selected;
    private boolean loading;
    private LinearLayout editor, beforeList, afterList, connectionPage, chainPage, xraysPage, logsPage;
    private ArrayAdapter<String> profileNames;
    private String sessionPin="";
    private int certificateRetryToken;
    private String importName="";
    private Spinner profilePicker, mode, appFilter;
    private TextView appFilterSummary;
    private EditText name,gateway,port,username,password,realm,certificate,listen,dns;
    private CheckBox remember,reconnect;
    private LinearLayout xrayList;
    private TextView status,log,flow,stats;
    private Button connect,disconnect;
    private JSONObject awaitingConfig;
    private String pendingExport;
    private static final int PICK_XRAY = 5;
    private String xrayTarget;
    private TextView xrayStatus;
    private boolean dark;
    private int teal=Color.rgb(17,109,102),ink=Color.rgb(28,41,62);
    private final BroadcastReceiver receiver=new BroadcastReceiver() {
        @Override public void onReceive(Context c,Intent intent) {
            String type=intent.getStringExtra("type"), message=intent.getStringExtra("message");
            if("stats".equals(type)) stats.setText(message); else { refreshStatus(); refreshLog(); }
            if("certificate".equals(type)) certificateDecision();
        }
    };
    @Override public void onCreate(Bundle saved) {
        dark=getSharedPreferences("appearance",MODE_PRIVATE).getBoolean("dark",true);
        setTheme(dark ? android.R.style.Theme_Material_NoActionBar : android.R.style.Theme_Material_Light_NoActionBar);
        ink=Color.parseColor(dark ? "#E4EDF7" : "#1C293E");
        teal=Color.parseColor(dark ? "#53D9C3" : "#116D66");
        super.onCreate(saved); getWindow().setStatusBarColor(Color.parseColor(dark?"#101823":"#F1F5F8")); getWindow().setNavigationBarColor(Color.parseColor(dark?"#101823":"#F1F5F8"));
        store=new ProfileStore(this);
        try { profiles=store.load(); } catch(Exception e) { profiles=new JSONArray(); try { profiles.put(ProfileStore.defaults()); } catch(Exception ignored) {} error("Saved profiles could not be loaded: "+e.getMessage()); }
        LinearLayout page=new LinearLayout(this); page.setOrientation(LinearLayout.VERTICAL); page.setBackgroundColor(Color.parseColor(dark?"#101823":"#F1F5F8")); page.setPadding(dp(18),dp(12),dp(18),dp(10));
        // Keep content below Android's system bars with target SDK 36 edge-to-edge behavior.
        page.setOnApplyWindowInsetsListener((view,insets)->{ view.setPadding(dp(18)+insets.getSystemWindowInsetLeft(),dp(12)+insets.getSystemWindowInsetTop(),dp(18)+insets.getSystemWindowInsetRight(),dp(10)+insets.getSystemWindowInsetBottom()); return insets; });
        LinearLayout header=row();
        ImageView icon=new ImageView(this);icon.setImageResource(getResources().getIdentifier("wayline","drawable",getPackageName()));
        header.addView(icon,new LinearLayout.LayoutParams(dp(42),dp(42)));
        TextView heading=text("Wayline",26,true); heading.setTextColor(teal); header.addView(heading,new LinearLayout.LayoutParams(0,-2,1));
        header.addView(button(dark?"☀":"☾",()->{
            if(TunnelService.running)throw new Exception("Disconnect before changing appearance.");
            saveCurrent();getSharedPreferences("appearance",MODE_PRIVATE).edit().putBoolean("dark",!dark).commit();recreate();
        }));
        page.addView(header); page.addView(text("Version "+appVersion()+" · VPN",13,false));
        status=text("Disconnected",15,true); page.addView(status); stats=text("",12,false); page.addView(stats);
        LinearLayout navigation=row();
        for(String title:new String[]{"Connection","Chain","Xrays","Logs"})navigation.addView(button(title,()->selectPage(title)),weighted());
        page.addView(navigation);
        ScrollView scroll=new ScrollView(this);scroll.setFillViewport(true);
        LinearLayout pages=new LinearLayout(this);pages.setOrientation(1);scroll.addView(pages);page.addView(scroll,new LinearLayout.LayoutParams(-1,0,1));
        connectionPage=newPage(pages);chainPage=newPage(pages);xraysPage=newPage(pages);logsPage=newPage(pages);
        editor=connectionPage;
        editor.addView(label("VPN profiles"));profilePicker=new Spinner(this);editor.addView(profilePicker);
        LinearLayout profileButtons=row();profileButtons.addView(button("New",()->newProfile()),weighted());profileButtons.addView(button("Rename",()->renameProfile()),weighted());profileButtons.addView(button("Save",()->{saveCurrent();android.widget.Toast.makeText(this,"Profile saved",0).show();}),weighted());editor.addView(profileButtons);
        name=field("Profile name",false,false);
        mode=new Spinner(this);mode.setAdapter(adapter(new String[]{"Proxy only","VPN · device traffic"}));editor.addView(label("Traffic mode"));editor.addView(mode);
        gateway=field("Gateway",false,false);port=field("Gateway port",false,true);username=field("Username",false,false);password=field("Password",true,false);
        remember=new CheckBox(this);remember.setText("Remember password securely");editor.addView(remember);
        reconnect=new CheckBox(this);reconnect.setText("Auto reconnect");editor.addView(reconnect);
        editor.addView(label("Apps using the VPN"));appFilter=new Spinner(this);appFilter.setAdapter(adapter(new String[]{"All apps","Only selected apps","Exclude selected apps"}));editor.addView(appFilter);appFilterSummary=text("",12,false);editor.addView(appFilterSummary);editor.addView(button("Choose apps",()->chooseFilteredApps()));editor.addView(text("Applies in VPN mode. Other apps use their normal connection.",12,false));
        LinearLayout advanced=new LinearLayout(this);advanced.setOrientation(1);advanced.setVisibility(View.GONE);
        Button expand=button("Advanced options  ▾",()->advanced.setVisibility(advanced.getVisibility()==View.GONE?View.VISIBLE:View.GONE));editor.addView(expand);editor.addView(advanced);
        editor=advanced;
        realm=field("Realm",false,false);listen=field("Local proxy port",false,true);certificate=field("Trusted SHA-256 fingerprint",false,false);dns=field("DNS override · IPv4 addresses",false,false);
        editor.addView(button("Manage trusted certificates",()->manageCertificates()));
        editor=connectionPage;
        LinearLayout files=row();files.addView(button("Import",()->importProfile()),weighted());files.addView(button("Export",()->exportProfile()),weighted());files.addView(button("Delete profile",()->deleteProfile()),weighted());editor.addView(files);
        editor=chainPage;editor.addView(text("Traffic chain",23,true));editor.addView(text("Enabled hops run from top to bottom. Add saved Xrays from the Xrays page.",13,false));
        beforeList=new LinearLayout(this);beforeList.setOrientation(1);afterList=new LinearLayout(this);afterList.setOrientation(1);
        editor.addView(label("Before VPN"));editor.addView(beforeList);editor.addView(button("Add entry proxy",()->editHop("before",-1)));
        editor.addView(label("After VPN"));editor.addView(afterList);editor.addView(button("Add exit proxy",()->editHop("after",-1)));
        flow=text("",13,true);flow.setPadding(dp(14),dp(14),dp(14),dp(14));GradientDrawable bg=new GradientDrawable();bg.setColor(Color.parseColor(dark?"#20352F":"#E3F4EF"));bg.setCornerRadius(dp(12));flow.setBackground(bg);editor.addView(flow);
        editor.addView(text("For a proxy running on this phone, use VPN mode and choose its app in the After hop settings. Local app routing requires Android 10+.",12,false));
        editor=xraysPage;editor.addView(text("Your Xrays",23,true));editor.addView(text("Save configurations by name. Enable one or combine several in your chain.",13,false));
        xrayStatus=text("",12,false);editor.addView(xrayStatus);xrayList=new LinearLayout(this);xrayList.setOrientation(1);editor.addView(xrayList);
        LinearLayout imports=row();imports.addView(button("Paste",()->pasteXray()),weighted());imports.addView(button("Share link",()->enterXray()),weighted());imports.addView(button("JSON file",()->pickXray()),weighted());editor.addView(imports);
        editor=logsPage;editor.addView(text("Connection logs",23,true));
        LinearLayout logActions=row();logActions.addView(button("Copy logs",()->copy(log.getText().toString())),weighted());logActions.addView(button("Clear logs",()->{synchronized(TunnelService.history){TunnelService.history.clear();}refreshLog();}),weighted());editor.addView(logActions);
        log=text("",12,false);log.setTypeface(Typeface.MONOSPACE);log.setTextIsSelectable(true);editor.addView(log);
        editor.addView(button("Copy certificate fingerprint",()->{if(TunnelService.certificate.isEmpty())throw new Exception("No certificate has been reported.");copy(TunnelService.certificate);}));
        editor=connectionPage;
        LinearLayout actions=row();connect=button("Connect",()->connect());connect.setTextColor(Color.parseColor("#101823"));connect.setBackgroundTintList(android.content.res.ColorStateList.valueOf(teal));disconnect=button("Disconnect",()->stopServiceSession());actions.addView(connect,weighted());actions.addView(disconnect,weighted());page.addView(actions);
        try{selectPage("Connection");}catch(Exception ignored){}
        setContentView(page);
        profilePicker.setOnItemSelectedListener(new android.widget.AdapterView.OnItemSelectedListener() {
            public void onNothingSelected(android.widget.AdapterView<?> p) {}
            public void onItemSelected(android.widget.AdapterView<?> p,View v,int position,long id) { if(!loading && position!=selected) attempt(()->{saveCurrent();loadProfile(position);}); }
        });
        mode.setOnItemSelectedListener(new android.widget.AdapterView.OnItemSelectedListener() {
            public void onNothingSelected(android.widget.AdapterView<?> p) {} public void onItemSelected(android.widget.AdapterView<?> p,View v,int n,long id) { updateFlow(); }
        });
        if (saved != null) xrayTarget=saved.getString("xrayTarget");
        refreshPicker(saved == null ? 0 : Math.min(saved.getInt("profileIndex"),profiles.length()-1)); refreshStatus(); refreshLog();
    }
    @Override protected void onSaveInstanceState(Bundle state) {
        super.onSaveInstanceState(state);
        state.putInt("profileIndex", selected);
        state.putString("xrayTarget", xrayTarget);
    }
    @Override protected void onResume() { super.onResume(); IntentFilter filter=new IntentFilter(TunnelService.EVENT); if(Build.VERSION.SDK_INT>=33) registerReceiver(receiver,filter,Context.RECEIVER_NOT_EXPORTED); else registerReceiver(receiver,filter); refreshStatus(); refreshLog(); }
    @Override protected void onPause() { unregisterReceiver(receiver); super.onPause(); }
    private LinearLayout newPage(LinearLayout parent){LinearLayout result=new LinearLayout(this);result.setOrientation(1);parent.addView(result,new LinearLayout.LayoutParams(-1,-2));return result;}
    private void selectPage(String title)throws Exception{if(current!=null&&!loading&&!TunnelService.running)saveCurrent();if(connectionPage==null)return;connectionPage.setVisibility("Connection".equals(title)?View.VISIBLE:View.GONE);chainPage.setVisibility("Chain".equals(title)?View.VISIBLE:View.GONE);xraysPage.setVisibility("Xrays".equals(title)?View.VISIBLE:View.GONE);logsPage.setVisibility("Logs".equals(title)?View.VISIBLE:View.GONE);}
    private void refreshProfileNames(){if(profileNames==null)return;loading=true;profileNames.setNotifyOnChange(false);profileNames.clear();for(int i=0;i<profiles.length();i++)profileNames.add(profiles.optJSONObject(i).optString("name","Connection"));profileNames.notifyDataSetChanged();profilePicker.setSelection(selected);loading=false;}
    private void renameProfile()throws Exception{EditText title=new EditText(this);title.setText(name.getText());AlertDialog dialog=new AlertDialog.Builder(this).setTitle("Rename VPN profile").setView(title).setNegativeButton("Cancel",null).setPositiveButton("Rename",null).create();dialog.setOnShowListener(d->dialog.getButton(-1).setOnClickListener(v->attempt(()->{String value=title.getText().toString().trim();if(value.isEmpty())throw new Exception("Enter a name.");name.setText(value);current.put("name",value);store.save(profiles);refreshProfileNames();dialog.dismiss();})));dialog.show();}
    private String guessXrayName(String text){try{String clean=text.trim().replace("\\://","://");if(clean.startsWith("vmess://")){String data=clean.substring(8);return new JSONObject(new String(android.util.Base64.decode(data,android.util.Base64.DEFAULT),StandardCharsets.UTF_8)).optString("ps","");}String fragment=Uri.parse(clean).getFragment();return fragment==null?"":fragment;}catch(Exception e){return "";}}
    private void manageCertificates()throws Exception{JSONArray trusted=current.optJSONArray("trustedCertificates");if(trusted==null||trusted.length()==0){error("No remembered certificate fingerprints for this profile.");return;}String[] values=new String[trusted.length()];for(int i=0;i<values.length;i++)values[i]=trusted.getString(i);new AlertDialog.Builder(this).setTitle("Trusted fingerprints · tap to remove").setItems(values,(d,n)->attempt(()->{trusted.remove(n);store.save(profiles);})).setNegativeButton("Done",null).show();}
    private void certificateDecision(){
        String fingerprint=TunnelService.certificate;if(!fingerprint.matches("[0-9a-f]{64}"))return;String profileId=current.optString("id");
        LinearLayout panel=new LinearLayout(this);panel.setPadding(dp(20),dp(8),dp(20),dp(8));panel.setOrientation(1);panel.addView(text("The gateway certificate could not be verified. Proceed only if you recognize or independently verified this SHA-256 fingerprint.",14,false));TextView value=text(fingerprint,13,true);value.setTextIsSelectable(true);panel.addView(value);CheckBox rememberTrust=new CheckBox(this);rememberTrust.setText("Remember this fingerprint for this VPN profile");panel.addView(rememberTrust);
        new AlertDialog.Builder(this).setTitle("Gateway certificate").setView(panel).setNegativeButton("Cancel",null).setNeutralButton("Copy",(d,w)->copy(fingerprint)).setPositiveButton("Proceed",(d,w)->attempt(()->{
            if(!profileId.equals(current.optString("id")))return;
            if(rememberTrust.isChecked()){JSONArray list=current.optJSONArray("trustedCertificates");if(list==null)list=new JSONArray();boolean found=false;for(int i=0;i<list.length();i++)found|=fingerprint.equals(list.optString(i));if(!found){if(list.length()>=31)throw new Exception("Remove an old fingerprint first.");list.put(fingerprint);}current.put("trustedCertificates",list);store.save(profiles);}else sessionPin=fingerprint;
            retryCertificate(profileId,0,++certificateRetryToken);
        })).show();
    }
    private void retryCertificate(String profileId,int attempt,int token){if(token!=certificateRetryToken)return;if(!profileId.equals(current.optString("id")))return;if(!TunnelService.running){attempt(()->connect());return;}if(attempt<150)new android.os.Handler(getMainLooper()).postDelayed(()->retryCertificate(profileId,attempt+1,token),100);else error("Disconnect the current session and connect again.");}
    private String appVersion() {
        try { return getPackageManager().getPackageInfo(getPackageName(),0).versionName; }
        catch(PackageManager.NameNotFoundException error) { return "unknown"; }
    }
    private int dp(int n) { return (int)(n*getResources().getDisplayMetrics().density+0.5f); }
    private TextView text(String s,int size,boolean bold) { TextView v=new TextView(this); v.setText(s); v.setTextSize(size); v.setTextColor(ink); v.setPadding(0,dp(5),0,dp(7)); if(bold)v.setTypeface(null,Typeface.BOLD); return v; }
    private TextView label(String s) { return text(s,14,true); }
    private LinearLayout row() { LinearLayout row=new LinearLayout(this); row.setOrientation(0); return row; }
    private LinearLayout.LayoutParams weighted() { LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(0,-2,1); p.setMargins(0,dp(3),dp(4),dp(3)); return p; }
    private Button button(String s,Work action) { Button b=new Button(this);b.setText(s);b.setAllCaps(false);b.setTextSize(13);b.setTextColor(ink);b.setMinHeight(dp(44));b.setMinimumWidth(dp(42));b.setPadding(dp(12),dp(8),dp(12),dp(8));
        GradientDrawable normal=new GradientDrawable();normal.setColor(Color.parseColor(dark?"#1D2937":"#FFFFFF"));normal.setCornerRadius(dp(12));normal.setStroke(dp(1),Color.parseColor(dark?"#334356":"#D5DFE8"));
        b.setBackground(new android.graphics.drawable.RippleDrawable(android.content.res.ColorStateList.valueOf(Color.parseColor(dark?"#40565B":"#C6E9E1")),normal,null));b.setOnClickListener(v->attempt(action));return b; }
    private EditText field(String title,boolean secret,boolean number) { editor.addView(label(title)); EditText v=new EditText(this); v.setSingleLine(true); v.setTextSize(15); v.setInputType(number?InputType.TYPE_CLASS_NUMBER:secret?InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_PASSWORD:InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD); editor.addView(v,new LinearLayout.LayoutParams(-1,-2)); return v; }
    private ArrayAdapter<String> adapter(String[] values) { ArrayAdapter<String> adapter=new ArrayAdapter<>(this,android.R.layout.simple_spinner_item,values); adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item); return adapter; }
    interface Work { void run() throws Exception; }
    private void attempt(Work work) { try { work.run(); } catch(Exception e) { error(e.getMessage()); } }
    private void error(String message) { new AlertDialog.Builder(this).setTitle("Wayline").setMessage(message==null?"Unexpected error.":message).setPositiveButton("OK",null).show(); }
    private void copy(String value) { getSystemService(ClipboardManager.class).setPrimaryClip(ClipData.newPlainText("Wayline certificate fingerprint",value)); }
    private void refreshPicker(int index) {
        loading=true; String[] names=new String[profiles.length()]; for(int i=0;i<names.length;i++)names[i]=profiles.optJSONObject(i).optString("name","Connection"); profileNames=adapter(names);profilePicker.setAdapter(profileNames); profilePicker.setSelection(index); loading=false; attempt(()->loadProfile(index));
    }
    private String readSecret(String id) { try{return store.getSecret(id);}catch(Exception e){ error("A saved credential is unavailable. Enter it again. "+e.getMessage()); return "";} }
    private void loadProfile(int index) throws Exception {
        selected=index; current=profiles.getJSONObject(index); loading=true;
        name.setText(current.optString("name")); gateway.setText(current.optString("gateway")); port.setText(""+current.optInt("port",443)); username.setText(current.optString("username")); realm.setText(current.optString("realm")); certificate.setText(current.optString("certificateSha256")); listen.setText(""+current.optInt("listenPort",1080)); mode.setSelection("vpn".equals(current.optString("mode"))?1:0); remember.setChecked(current.optBoolean("rememberPassword")); password.setText(current.optBoolean("rememberPassword")?readSecret(current.getString("id")):"");
        JSONArray a=current.optJSONArray("dns"); StringBuilder servers=new StringBuilder(); if(a!=null)for(int i=0;i<a.length();i++){if(i>0)servers.append(", ");servers.append(a.getString(i));}dns.setText(servers.toString());
        reconnect.setChecked(current.optBoolean("autoReconnect"));String filter=current.optString("appFilterMode","all");appFilter.setSelection("include".equals(filter)?1:"exclude".equals(filter)?2:0);refreshAppSummary();
        showXray();
        loading=false; refreshHops(); updateFlow();
    }
    private void newProfile() throws Exception { saveCurrent();JSONObject p=ProfileStore.defaults(); p.put("name","New connection"); p.put("gateway",""); p.put("username",""); profiles.put(p);store.save(profiles); refreshPicker(profiles.length()-1); }
    private void deleteProfile() { if(TunnelService.running)return; new AlertDialog.Builder(this).setTitle("Delete profile?").setMessage("Remove this profile and its credentials from this device?").setNegativeButton("Cancel",null).setPositiveButton("Delete",(d,w)->attempt(()->{JSONArray removed=xrays();store.remove(current); profiles.remove(selected); if(profiles.length()==0)profiles.put(ProfileStore.defaults());store.save(profiles);for(int i=0;i<removed.length();i++){File file=xrayFile(removed.getJSONObject(i));if(file!=null)file.delete();}refreshPicker(0);})).show(); }
    private int number(EditText field) throws Exception { int n;try{n=Integer.parseInt(field.getText().toString());}catch(Exception e){throw new Exception("Enter a valid port number.");}if(n<1||n>65535)throw new Exception("Ports must be from 1 to 65535.");return n; }
    private void saveCurrent() throws Exception {
        if(TunnelService.running)throw new Exception("Disconnect before editing profiles.");
        if(name.getText().toString().trim().isEmpty())throw new Exception("Enter a profile name.");
        if(!current.optString("gateway").equals(gateway.getText().toString().trim())||current.optInt("port",443)!=number(port))current.remove("trustedCertificates");
        current.put("name",name.getText().toString().trim()).put("gateway",gateway.getText().toString().trim()).put("port",number(port)).put("username",username.getText().toString().trim()).put("realm",realm.getText().toString().trim()).put("certificateSha256",certificate.getText().toString().trim()).put("mode",mode.getSelectedItemPosition()==1?"vpn":"proxy").put("listenPort",number(listen)).put("rememberPassword",remember.isChecked());
        JSONArray servers=new JSONArray();for(String s:dns.getText().toString().split("[,;\\s]+"))if(!s.isEmpty())servers.put(s);current.put("dns",servers);
        current.put("autoReconnect",reconnect.isChecked());current.put("appFilterMode",appFilter.getSelectedItemPosition()==1?"include":appFilter.getSelectedItemPosition()==2?"exclude":"all");
        store.putSecret(current.getString("id"),remember.isChecked()?password.getText().toString():"");
        for(String side:new String[]{"before","after"}) { JSONArray hops=hops(side);for(int i=0;i<hops.length();i++){JSONObject h=hops.getJSONObject(i);if(h.has("password")){String secret=h.optString("password");store.putSecret(h.getString("id"),secret);h.put("rememberPassword",!secret.isEmpty());h.remove("password");}} }
        store.save(profiles);refreshProfileNames();
    }
    private JSONArray hops(String side) throws Exception { JSONArray a=current.optJSONArray(side);if(a==null){a=new JSONArray();current.put(side,a);}return a; }
    private void refreshHops() throws Exception { renderHops("before",beforeList);renderHops("after",afterList);updateFlow(); }
    private void renderHops(String side,LinearLayout panel) throws Exception {
        panel.removeAllViews();JSONArray a=hops(side);
        if(a.length()==0)panel.addView(text("None",12,false));
        for(int i=0;i<a.length();i++){
            final int index=i;
            JSONObject h=a.getJSONObject(i);
            LinearLayout row=row();
            CheckBox enabled=new CheckBox(this);
            enabled.setChecked(h.optBoolean("enabled",true));
            enabled.setContentDescription("Enable "+h.optString("host")+" proxy");
            enabled.setOnCheckedChangeListener((v,checked)->attempt(()->{h.put("enabled",checked);store.save(profiles);updateFlow();renderXrays();}));
            row.addView(enabled);
            boolean managed="xray".equals(h.optString("type"));
            String title=managed?"Xray · "+xrayEntry(h.optString("xrayId")).optString("name","Missing configuration"):h.optString("type","socks5")+" "+h.optString("host")+":"+h.optInt("port");
            Button edit=button((i+1)+". "+title,()->{if(managed)editXray(h.optString("xrayId"));else editHop(side,index);});
            row.addView(edit,new LinearLayout.LayoutParams(0,-2,1));
            row.addView(button("↑",()->{if(index>0){JSONArray source=hops(side),reordered=new JSONArray();for(int j=0;j<source.length();j++)reordered.put(source.get(j==index-1?index:j==index?index-1:j));current.put(side,reordered);store.save(profiles);refreshHops();}}));
            row.addView(button("×",()->{JSONObject removed=hops(side).getJSONObject(index);store.putSecret(removed.optString("id"),"");hops(side).remove(index);store.save(profiles);refreshHops();}));
            panel.addView(row);
        }
    }
    private void editHop(String side,int index) throws Exception {
        if(TunnelService.running)return;
        JSONObject existing=index>=0?hops(side).getJSONObject(index):new JSONObject().put("id",ProfileStore.id()).put("type","socks5").put("host","before".equals(side)?"127.0.0.1":"").put("port",1080);
        LinearLayout box=new LinearLayout(this);box.setOrientation(1);box.setPadding(dp(20),0,dp(20),0);
        Spinner type=new Spinner(this);type.setAdapter(adapter(new String[]{"socks5","http","https"}));type.setSelection("https".equals(existing.optString("type"))?2:"http".equals(existing.optString("type"))?1:0);box.addView(type);
        EditText host=new EditText(this),p=new EditText(this),u=new EditText(this),secret=new EditText(this); host.setHint("Host");host.setText(existing.optString("host"));p.setHint("Port");p.setInputType(InputType.TYPE_CLASS_NUMBER);p.setText(""+existing.optInt("port",1080));u.setHint("Username (optional)");u.setText(existing.optString("username"));secret.setHint("Password (optional)");secret.setInputType(InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_PASSWORD);secret.setText(existing.has("password")?existing.optString("password"):existing.optBoolean("rememberPassword")?readSecret(existing.optString("id")):"");box.addView(host);box.addView(p);box.addView(u);box.addView(secret);box.addView(text("Hop passwords are encrypted with Android Keystore when you save this profile.",12,false));
        EditText packageName=new EditText(this);
        packageName.setHint("Android exit proxy app package (local After only)");
        packageName.setText(existing.optString("androidPackage"));
        if("after".equals(side)){
            box.addView(packageName);
            box.addView(button("Choose local proxy app",()->chooseProxyApp(packageName)));
        }
        AlertDialog dialog=new AlertDialog.Builder(this).setTitle(index<0?"Add proxy hop":"Edit proxy hop").setView(box).setNegativeButton("Cancel",null).setPositiveButton("Save",null).create();
        dialog.setOnShowListener(d->dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(v->attempt(()->{if(host.getText().toString().trim().isEmpty())throw new Exception("Enter a proxy host.");JSONObject h=new JSONObject(existing.toString()).put("type",type.getSelectedItem().toString()).put("host",host.getText().toString().trim()).put("port",number(p)).put("username",u.getText().toString()).put("password",secret.getText().toString()).put("androidPackage",packageName.getText().toString().trim());if(index<0)hops(side).put(h);else hops(side).put(index,h);refreshHops();dialog.dismiss();})));dialog.show();
    }
    private void chooseProxyApp(EditText field){java.util.LinkedHashSet<String> selected=new java.util.LinkedHashSet<>();if(field.length()>0)selected.add(field.getText().toString());AppPicker.show(this,"Local proxy app",selected,false,packages->{if(!packages.isEmpty())field.setText(packages.iterator().next());});}
    private void refreshAppSummary(){JSONArray apps=current.optJSONArray("appFilterApps");appFilterSummary.setText((apps==null?0:apps.length())+" apps selected");}
    private void chooseFilteredApps(){java.util.LinkedHashSet<String> selected=new java.util.LinkedHashSet<>();JSONArray apps=current.optJSONArray("appFilterApps");if(apps!=null)for(int i=0;i<apps.length();i++)selected.add(apps.optString(i));AppPicker.show(this,"VPN app selection",selected,true,packages->attempt(()->{JSONArray result=new JSONArray();for(String value:packages)result.put(value);current.put("appFilterApps",result);store.save(profiles);refreshAppSummary();}));}
    private int enabledCount(String side){JSONArray a=current.optJSONArray(side);int count=0;if(a!=null)for(int i=0;i<a.length();i++)if(a.optJSONObject(i).optBoolean("enabled",true))count++;return count;}
    private void updateFlow(){
        if(current==null||flow==null)return;
        StringBuilder path=new StringBuilder(mode.getSelectedItemPosition()==1?"Device":"Apps");
        for(String side:new String[]{"before","after"}){
            JSONArray chain=current.optJSONArray(side);
            if(chain!=null)for(int i=0;i<chain.length();i++){JSONObject h=chain.optJSONObject(i);if(h.optBoolean("enabled",true)){String title="xray".equals(h.optString("type"))?xrayName(h.optString("xrayId")):h.optString("host")+":"+h.optInt("port");path.append(" → ").append(title);}}
            if("before".equals(side))path.append(" → VPN");
        }
        flow.setText(path.append(" → Destination").toString());
    }
    private String xrayName(String id){try{return xrayEntry(id).optString("name","Xray");}catch(Exception e){return "Missing Xray";}}

    private JSONObject wireHop(JSONObject h)throws Exception{
        if("xray".equals(h.optString("type"))) {
            JSONObject entry=xrayEntry(h.optString("xrayId"));
            File file=xrayFile(entry);
            if(file==null || !file.isFile())throw new Exception("The selected Xray file is missing. Import it again.");
            File core=new File(getApplicationInfo().nativeLibraryDir,"libxray.so");
            if(!core.isFile())throw new Exception("This build is missing its Xray core.");
            return new JSONObject().put("type","xray").put("xray",new JSONObject()
                .put("executable",core.getAbsolutePath()).put("configFile",file.getAbsolutePath()).put("outboundTag",entry.optString("outboundTag")));
        }
        return new JSONObject().put("type",h.optString("type","socks5")).put("host",h.getString("host")).put("port",h.getInt("port")).put("username",h.optString("username")).put("password",h.optBoolean("rememberPassword")?store.getSecret(h.getString("id")):"").put("androidPackage",h.optString("androidPackage"));
    }
    private void connect() throws Exception {
        if(TunnelService.running)return;saveCurrent();if(current.optString("gateway").isEmpty()||current.optString("username").isEmpty())throw new Exception("Enter a gateway and username.");if(password.getText().length()==0)throw new Exception("Enter your VPN password.");
        JSONObject config=new JSONObject();for(String key:new String[]{"gateway","port","username","realm","certificateSha256","mode","listenPort","dns","autoReconnect"})config.put(key,current.get(key));config.put("password",password.getText().toString()).put("tunName","Wayline");config.put("appFilterMode",current.optString("appFilterMode","all")).put("appFilterApps",current.optJSONArray("appFilterApps"));config.put("trustedCertificates",current.optJSONArray("trustedCertificates"));if(!sessionPin.isEmpty()){config.put("certificateSha256",sessionPin);sessionPin="";}
        for(String side:new String[]{"before","after"}){JSONArray result=new JSONArray(),a=hops(side);for(int i=0;i<a.length();i++)if(a.getJSONObject(i).optBoolean("enabled",true))result.put(wireHop(a.getJSONObject(i)));config.put(side,result);}
        JSONArray bootstrap=new JSONArray(); ConnectivityManager cm=getSystemService(ConnectivityManager.class);LinkProperties lp=cm.getLinkProperties(cm.getActiveNetwork());if(lp!=null)for(InetAddress address:lp.getDnsServers())bootstrap.put(address.getHostAddress());config.put("bootstrapDns",bootstrap);
        awaitingConfig=config;
        if("vpn".equals(config.getString("mode"))){Intent permission=VpnService.prepare(this);if(permission!=null){startActivityForResult(permission,1);return;}}
        notificationThenStart();
    }
    private void notificationThenStart(){if(Build.VERSION.SDK_INT>=33&&checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED){requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},4);return;}startConnection();}
    private void startConnection(){if(awaitingConfig==null)return;TunnelService.pendingConfig=awaitingConfig.toString();awaitingConfig=null;startForegroundService(new Intent(this,TunnelService.class));status.setText("Connecting");connect.setEnabled(false);}
    @Override public void onRequestPermissionsResult(int request,String[] permissions,int[] results){super.onRequestPermissionsResult(request,permissions,results);if(request==4)startConnection();}
    private void stopServiceSession(){certificateRetryToken++;sessionPin="";if(TunnelService.running)startService(new Intent(this,TunnelService.class).setAction(TunnelService.STOP));}
    private void refreshStatus(){if(status==null)return;status.setText(TunnelService.lastStatus);connect.setEnabled(!TunnelService.running);disconnect.setEnabled(TunnelService.running);enable(connectionPage,!TunnelService.running);enable(chainPage,!TunnelService.running);enable(xraysPage,!TunnelService.running);}
    private void enable(View view,boolean value){view.setEnabled(value);if(view instanceof ViewGroup)for(int i=0;i<((ViewGroup)view).getChildCount();i++)enable(((ViewGroup)view).getChildAt(i),value);}
    private void refreshLog(){if(log==null)return;StringBuilder b=new StringBuilder();synchronized(TunnelService.history){for(String line:TunnelService.history)b.append(line).append('\n');}log.setText(b.toString());}
    private void importProfile(){Intent intent=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("application/json").addCategory(Intent.CATEGORY_OPENABLE);startActivityForResult(intent,2);}
    private File xrayFile(JSONObject profile) {
        String id=profile.optString("xrayConfigId");
        return id.matches("[0-9a-f-]{36}") ? new File(getFilesDir(),"xray-"+id+".json") : null;
    }
    private void showXray() {
        attempt(()->renderXrays());
        updateFlow();
    }
    private JSONArray xrays() throws Exception {
        JSONArray list=current.optJSONArray("xrays");
        if(list==null) {
            list=new JSONArray();
            String legacy=current.optString("xrayConfigId");
            if(!legacy.isEmpty()) {
                list.put(new JSONObject().put("id",legacy).put("xrayConfigId",legacy).put("name","Imported Xray").put("outboundTag",current.optString("xrayOutboundTag")));
                hops("after").put(new JSONObject().put("id",ProfileStore.id()).put("type","xray").put("xrayId",legacy).put("enabled",true));
                current.remove("xrayConfigId");current.remove("xrayOutboundTag");
            }
            current.put("xrays",list);
        }
        return list;
    }
    private JSONObject xrayEntry(String id)throws Exception{
        JSONArray list=xrays();
        for(int i=0;i<list.length();i++)if(id.equals(list.getJSONObject(i).optString("id")))return list.getJSONObject(i);
        throw new Exception("Saved Xray configuration is missing.");
    }
    private boolean xrayEnabled(String id)throws Exception{
        for(String side:new String[]{"before","after"}){
            JSONArray list=hops(side);
            for(int i=0;i<list.length();i++)if(id.equals(list.getJSONObject(i).optString("xrayId")) && list.getJSONObject(i).optBoolean("enabled",true))return true;
        }
        return false;
    }
    private void renderXrays()throws Exception{
        if(xrayList==null)return;xrayList.removeAllViews();JSONArray list=xrays();int activeCount=0;
        for(int i=0;i<list.length();i++){
            JSONObject entry=list.getJSONObject(i);String id=entry.getString("id");boolean active=xrayEnabled(id);if(active)activeCount++;
            LinearLayout card=new LinearLayout(this);card.setOrientation(1);card.setPadding(dp(14),dp(12),dp(14),dp(12));GradientDrawable bg=new GradientDrawable();bg.setColor(Color.parseColor(dark?"#172332":"#FFFFFF"));bg.setCornerRadius(dp(16));bg.setStroke(dp(1),Color.parseColor(dark?"#304254":"#D5DFE8"));card.setBackground(bg);
            LinearLayout top=row();CheckBox toggle=new CheckBox(this);toggle.setText(entry.optString("name","Xray"));toggle.setTextSize(17);toggle.setTypeface(null,Typeface.BOLD);toggle.setChecked(active);top.addView(toggle,new LinearLayout.LayoutParams(-1,-2));card.addView(top);
            StringBuilder positions=new StringBuilder();for(String side:new String[]{"before","after"}){JSONArray chain=hops(side);for(int j=0;j<chain.length();j++){JSONObject hop=chain.getJSONObject(j);if(id.equals(hop.optString("xrayId")))positions.append(side.equals("before")?"Entry #":"Exit #").append(j+1).append(hop.optBoolean("enabled",true)?" · On   ":" · Off   ");}}
            card.addView(text(positions.length()==0?"Saved · not in chain":positions.toString(),12,false));
            try{JSONObject cfg=readXray(entry),out=selectedOutbound(cfg,entry.optString("outboundTag")),server=xrayServer(out);card.addView(text(out.optString("protocol").toUpperCase()+" · "+server.optString("address")+":"+server.optInt("port"),13,false));}catch(Exception ignored){}
            toggle.setOnCheckedChangeListener((v,checked)->attempt(()->{boolean found=false;for(String side:new String[]{"before","after"}){JSONArray chain=hops(side);for(int j=0;j<chain.length();j++)if(id.equals(chain.getJSONObject(j).optString("xrayId"))){chain.getJSONObject(j).put("enabled",checked);found=true;}}if(checked&&!found)addXrayHop(id,"after");store.save(profiles);refreshHops();renderXrays();}));
            LinearLayout actions=row();actions.addView(button("Settings",()->editXray(id)),weighted());actions.addView(button("Add to chain",()->new AlertDialog.Builder(this).setTitle("Place "+entry.optString("name")).setItems(new String[]{"Before VPN · entry","After VPN · exit"},(d,n)->attempt(()->{addXrayHop(id,n==0?"before":"after");store.save(profiles);refreshHops();renderXrays();})).show()),weighted());actions.addView(button("Delete",()->deleteXray(id)),weighted());card.addView(actions);
            LinearLayout.LayoutParams layout=new LinearLayout.LayoutParams(-1,-2);layout.setMargins(0,dp(6),0,dp(10));xrayList.addView(card,layout);
        }
        xrayStatus.setText(list.length()+" saved · "+activeCount+" enabled");if(list.length()==0)xrayList.addView(text("Your library is empty. Paste a share link or import a JSON file below.",14,false));
    }
    private void addXrayHop(String id,String side)throws Exception{
        hops(side).put(new JSONObject().put("id",ProfileStore.id()).put("type","xray").put("xrayId",id).put("enabled",true));
    }
    private JSONObject readXray(JSONObject entry)throws Exception{return new JSONObject(new String(java.nio.file.Files.readAllBytes(xrayFile(entry).toPath()),StandardCharsets.UTF_8));}
    private JSONObject selectedOutbound(JSONObject config,String tag)throws Exception{JSONArray outs=config.getJSONArray("outbounds");for(int i=0;i<outs.length();i++){JSONObject out=outs.getJSONObject(i);if(!tag.isEmpty()?tag.equals(out.optString("tag")):!"freedom".equals(out.optString("protocol"))&&!"blackhole".equals(out.optString("protocol")))return out;}throw new Exception("Select an outbound tag.");}
    private JSONObject xrayServer(JSONObject out)throws Exception{JSONObject settings=out.getJSONObject("settings");JSONArray servers=settings.optJSONArray("vnext");if(servers==null)servers=settings.getJSONArray("servers");return servers.getJSONObject(0);}
    private EditText setting(LinearLayout panel,String title,String value,boolean secret){panel.addView(label(title));EditText field=new EditText(this);field.setText(value);field.setTextSize(14);field.setSingleLine(true);if(secret)field.setInputType(129);panel.addView(field);return field;}
    private void editXray(String id)throws Exception{
        if(TunnelService.running)return;JSONObject entry=xrayEntry(id),config=readXray(entry),out=selectedOutbound(config,entry.optString("outboundTag")),server=xrayServer(out);
        ScrollView scroll=new ScrollView(this);LinearLayout panel=new LinearLayout(this);panel.setOrientation(1);panel.setPadding(dp(20),0,dp(20),dp(12));scroll.addView(panel);
        EditText title=setting(panel,"Configuration name",entry.optString("name"),false),address=setting(panel,"Server",server.optString("address"),false),serverPort=setting(panel,"Port",String.valueOf(server.optInt("port")),false);
        JSONObject account=server;JSONArray users=server.optJSONArray("users");if(users!=null&&users.length()>0)account=users.getJSONObject(0);final JSONObject credentials=account;String credentialKey=account.has("id")?"id":"password";
        EditText credential=setting(panel,"id".equals(credentialKey)?"User ID":"Password",account.optString(credentialKey),true),tag=setting(panel,"Outbound tag",out.optString("tag"),false);
        panel.addView(text("Protocol: "+out.optString("protocol")+". Open JSON settings for transport, TLS, Reality and other protocol options.",12,false));
        final AlertDialog[] holder=new AlertDialog[1];panel.addView(button("Edit all JSON settings",()->{holder[0].dismiss();editXrayJson(entry,config); }));
        AlertDialog dialog=new AlertDialog.Builder(this).setTitle("Xray settings").setView(scroll).setNegativeButton("Cancel",null).setPositiveButton("Save settings",null).create();dialog.setOnShowListener(d->dialog.getButton(-1).setOnClickListener(v->attempt(()->{
            int n=Integer.parseInt(serverPort.getText().toString());if(address.getText().toString().trim().isEmpty()||n<1||n>65535||title.getText().toString().trim().isEmpty())throw new Exception("Enter a name, server and valid port.");
            server.put("address",address.getText().toString().trim()).put("port",n);if(credentials.has(credentialKey)||credential.getText().length()>0)credentials.put(credentialKey,credential.getText().toString());out.put("tag",tag.getText().toString().trim());config.put("remarks",title.getText().toString().trim());writeXray(entry,config);entry.put("name",title.getText().toString().trim()).put("outboundTag",tag.getText().toString().trim());store.save(profiles);renderXrays();refreshHops();dialog.dismiss();
        })));holder[0]=dialog;dialog.show();
    }
    private void writeXray(JSONObject entry,JSONObject config)throws Exception{File file=xrayFile(entry),temp=new File(file.getPath()+".tmp");try(FileOutputStream stream=new FileOutputStream(temp)){stream.write(config.toString(2).getBytes(StandardCharsets.UTF_8));stream.getFD().sync();}java.nio.file.Files.move(temp.toPath(),file.toPath(),java.nio.file.StandardCopyOption.REPLACE_EXISTING);}
    private void editXrayJson(JSONObject entry,JSONObject config)throws Exception{
        EditText json=new EditText(this);json.setText(config.toString(2));json.setTypeface(Typeface.MONOSPACE);json.setTextSize(12);json.setMinLines(8);json.setMaxLines(16);json.setGravity(android.view.Gravity.TOP);
        AlertDialog dialog=new AlertDialog.Builder(this).setTitle("JSON settings · "+entry.optString("name")).setView(json).setNegativeButton("Cancel",null).setPositiveButton("Save JSON",null).create();dialog.setOnShowListener(d->dialog.getButton(-1).setOnClickListener(v->attempt(()->{JSONObject updated=new JSONObject(json.getText().toString());selectedOutbound(updated,entry.optString("outboundTag"));writeXray(entry,updated);renderXrays();dialog.dismiss();})));dialog.show();
    }
    private void deleteXray(String id)throws Exception{
        JSONObject entry=xrayEntry(id);
        new AlertDialog.Builder(this).setTitle("Delete saved Xray?").setMessage("Remove this configuration and its chain hops?").setNegativeButton("Cancel",null).setPositiveButton("Delete",(d,w)->attempt(()->{
            JSONArray list=xrays();for(int i=list.length()-1;i>=0;i--)if(id.equals(list.getJSONObject(i).optString("id")))list.remove(i);
            for(String side:new String[]{"before","after"}){JSONArray chain=hops(side);for(int i=chain.length()-1;i>=0;i--)if(id.equals(chain.getJSONObject(i).optString("xrayId")))chain.remove(i);}
            store.save(profiles);File file=xrayFile(entry);if(file!=null)file.delete();renderXrays();refreshHops();
        })).show();
    }
    private void pickXray() throws Exception {
        if(TunnelService.running) return;
        xrayTarget=current.getString("id");
        Intent intent=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*").addCategory(Intent.CATEGORY_OPENABLE);
        startActivityForResult(intent,PICK_XRAY);
    }
    private void importXray(Uri uri, String target) throws Exception {
        if(TunnelService.running || target==null || !target.equals(current.optString("id")))
            throw new Exception("Select the original profile and import the Xray JSON again.");
        ByteArrayOutputStream bytes=new ByteArrayOutputStream();
        try(InputStream in=getContentResolver().openInputStream(uri)) {
            if(in==null) throw new Exception("Could not open the Xray JSON.");
            byte[] buffer=new byte[4096];
            int count;
            while((count=in.read(buffer))!=-1) {
                if(bytes.size()+count>1024*1024) throw new Exception("Xray JSON exceeds 1 MiB.");
                bytes.write(buffer,0,count);
            }
        }
        importName="";importXrayText(bytes.toString("UTF-8"),target);
    }
    private void pasteXray() throws Exception {
        ClipboardManager clipboard=getSystemService(ClipboardManager.class);
        if(!clipboard.hasPrimaryClip() || clipboard.getPrimaryClip().getItemCount()==0)
            throw new Exception("Copy Xray JSON or a share link first.");
        CharSequence text=clipboard.getPrimaryClip().getItemAt(0).coerceToText(this);
        importXrayText(text==null?"":text.toString(),current.getString("id"));
    }
    private void enterXray() {
        EditText text=new EditText(this);
        text.setHint("vmess://, vless://, trojan://, ss:// or Xray JSON");
        text.setMinLines(3);text.setMaxLines(8);
        AlertDialog dialog=new AlertDialog.Builder(this).setTitle("Import Xray")
            .setView(text).setNegativeButton("Cancel",null).setPositiveButton("Import",null).create();
        dialog.setOnShowListener(d->dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(v->attempt(()->{
            importXrayText(text.getText().toString(),current.getString("id"));dialog.dismiss();
        })));
        dialog.show();
    }
    private void importXrayText(String text,String target) throws Exception {
        if(TunnelService.running || text.isEmpty() || text.getBytes(StandardCharsets.UTF_8).length>1024*1024)
            throw new Exception("Disconnect and enter Xray JSON or a share link up to 1 MiB.");
        importName=guessXrayName(text);xrayStatus.setText("Importing Xray…");
        new Thread(()->{
            try {
                File engine=new File(getApplicationInfo().nativeLibraryDir,"libwayline.so");
                java.lang.Process child=new ProcessBuilder(engine.getAbsolutePath(),"--import-xray").start();
                FutureTask<byte[]> output=new FutureTask<>(()->{
                    try(InputStream in=child.getInputStream();ByteArrayOutputStream bytes=new ByteArrayOutputStream()){
                        byte[] block=new byte[4096];int n;
                        while((n=in.read(block))!=-1){
                            if(bytes.size()+n>1024*1024)throw new Exception("Imported config exceeds 1 MiB.");
                            bytes.write(block,0,n);
                        }
                        return bytes.toByteArray();
                    }
                });
                new Thread(output,"Wayline-import-output").start();
                byte[] canonical;
                try {
                    try(OutputStream input=child.getOutputStream()){input.write(text.getBytes(StandardCharsets.UTF_8));}
                    if(!child.waitFor(10,TimeUnit.SECONDS) || child.exitValue()!=0)
                        throw new Exception("Choose Xray JSON or a supported vmess://, vless://, trojan:// or ss:// link. TLS verification must be enabled.");
                    canonical=output.get(2,TimeUnit.SECONDS);
                } finally { child.destroy(); }
                runOnUiThread(()->attempt(()->storeXray(canonical,target)));
            } catch(Exception failure){
                runOnUiThread(()->{showXray();error(failure.getMessage());});
            }
        },"Wayline-Xray-import").start();
    }
    private void storeXray(byte[] canonical,String target) throws Exception {
        if(TunnelService.running || !target.equals(current.optString("id")))
            throw new Exception("Select the original profile and import again.");
        String id=ProfileStore.id();
        File imported=new File(getFilesDir(),"xray-"+id+".json");
        try {
            try(FileOutputStream out=new FileOutputStream(imported)) {
                out.write(canonical);
                out.getFD().sync();
            }
            JSONArray list=xrays();
            list.put(new JSONObject().put("id",id).put("xrayConfigId",id).put("name",new JSONObject(new String(canonical,StandardCharsets.UTF_8)).optString("remarks",importName).trim().isEmpty()?"Xray "+(list.length()+1):new JSONObject(new String(canonical,StandardCharsets.UTF_8)).optString("remarks",importName)).put("outboundTag",""));
            addXrayHop(id,"after");
            store.save(profiles);
        } catch(Exception failure) {
            JSONArray entries=xrays();
            for(int i=entries.length()-1;i>=0;i--)if(id.equals(entries.getJSONObject(i).optString("id")))entries.remove(i);
            for(String side:new String[]{"before","after"}){
                JSONArray chain=hops(side);
                for(int i=chain.length()-1;i>=0;i--)if(id.equals(chain.getJSONObject(i).optString("xrayId")))chain.remove(i);
            }
            imported.delete();
            throw failure;
        }
        showXray();refreshHops();
    }
    private void clearXray() throws Exception {
        if(TunnelService.running)return;
        for(String side:new String[]{"before","after"}){
            JSONArray chain=hops(side);
            for(int i=0;i<chain.length();i++)if("xray".equals(chain.getJSONObject(i).optString("type")))chain.getJSONObject(i).put("enabled",false);
        }
        store.save(profiles);renderXrays();refreshHops();
    }
    private void exportProfile()throws Exception{saveCurrent();new AlertDialog.Builder(this).setTitle("Export profile").setMessage("Passwords are always excluded. Remove usernames too?").setPositiveButton("Remove usernames",(d,w)->attempt(()->exportFile(true))).setNegativeButton("Keep usernames",(d,w)->attempt(()->exportFile(false))).setNeutralButton("Cancel",null).show();}
    private void exportFile(boolean redact)throws Exception{pendingExport=ProfileStore.exported(current,redact).toString(2);Intent intent=new Intent(Intent.ACTION_CREATE_DOCUMENT).setType("application/json").addCategory(Intent.CATEGORY_OPENABLE).putExtra(Intent.EXTRA_TITLE,"wayline-profile.json");startActivityForResult(intent,3);}
    @Override protected void onActivityResult(int request,int result,Intent data){super.onActivityResult(request,result,data);
        if(request==PICK_XRAY) {
            String target=xrayTarget;
            xrayTarget=null;
            if(result==RESULT_OK && data!=null && data.getData()!=null)
                attempt(()->importXray(data.getData(),target));
            return;
        }
        if(request==1){if(result==RESULT_OK)notificationThenStart();else{awaitingConfig=null;status.setText("VPN permission was declined.");}return;}if(result!=RESULT_OK||data==null||data.getData()==null)return;attempt(()->{if(request==2){try(InputStream in=getContentResolver().openInputStream(data.getData())){ByteArrayOutputStream bytes=new ByteArrayOutputStream();byte[] b=new byte[4096];int n;while((n=in.read(b))!=-1){if(bytes.size()+n>1024*1024)throw new Exception("Profile file is too large.");bytes.write(b,0,n);}String json=bytes.toString("UTF-8");JSONObject p=ProfileStore.imported(json.trim().startsWith("[")?new JSONArray(json).getJSONObject(0):new JSONObject(json));profiles.put(p);store.save(profiles);refreshPicker(profiles.length()-1);}}else if(request==3&&pendingExport!=null){try(OutputStream out=getContentResolver().openOutputStream(data.getData())){out.write(pendingExport.getBytes(StandardCharsets.UTF_8));}pendingExport=null;}});}
}
