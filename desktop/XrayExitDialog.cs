using System.IO;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Microsoft.Win32;

namespace Wayline;

public sealed record XrayExit(string Executable, string ConfigFile, string OutboundTag);

public sealed class XrayExitDialog : Window
{
    public XrayExit? Value { get; private set; }
    public string SavedName { get; private set; } = "";
    readonly TextBox name=new(), executable=new(), configPath=new(), tag=new(), address=new(), port=new(), identity=new(), network=new(), security=new(), sni=new(), json=new(){AcceptsReturn=true,AcceptsTab=true,FontFamily=new FontFamily("Consolas"),FontSize=12,VerticalScrollBarVisibility=ScrollBarVisibility.Auto};
    JsonObject? config;
    JsonObject? outbound, server, account;
    readonly TabControl tabs=new();
    int previousTab;
    bool updating;
    public XrayExitDialog(XrayExit? existing,string label="")
    {
        Title="Xray settings";Width=660;Height=700;MinHeight=480;WindowStartupLocation=WindowStartupLocation.CenterOwner;
        var root=new DockPanel {Margin=new Thickness(24)};Content=root;
        var save=new Button {Content="Save configuration",Padding=new Thickness(16,10,16,10),Background=AppTheme.Accent,Foreground=AppTheme.Background,IsDefault=true,Margin=new Thickness(0,14,0,0)};DockPanel.SetDock(save,Dock.Bottom);root.Children.Add(save);
        var details=new StackPanel();
        void Field(string title,Control box){details.Children.Add(new TextBlock {Text=title,Foreground=AppTheme.Muted,Margin=new Thickness(0,10,0,5)});box.Padding=new Thickness(9);details.Children.Add(box);}
        name.Text=label;executable.Text=existing?.Executable??"";configPath.Text=existing?.ConfigFile??"";tag.Text=existing?.OutboundTag??"";
        Field("Configuration name",name);Field("Xray executable",executable);
        var fileActions=new WrapPanel();var browseExe=new Button {Content="Choose executable",Padding=new Thickness(10),Margin=new Thickness(0,6,6,0)};browseExe.Click+=(_,_)=>{var picker=new OpenFileDialog {Filter="Executable|*.exe|All files|*.*"};if(picker.ShowDialog(this)==true)executable.Text=picker.FileName;};fileActions.Children.Add(browseExe);
        var browseConfig=new Button {Content="Load JSON file",Padding=new Thickness(10),Margin=new Thickness(0,6,0,0)};browseConfig.Click+=(_,_)=>{var picker=new OpenFileDialog {Filter="Xray JSON|*.json"};if(picker.ShowDialog(this)==true){configPath.Text=picker.FileName;TryLoad();}};fileActions.Children.Add(browseConfig);details.Children.Add(fileActions);
        Field("Outbound tag",tag);var loadTag=new Button {Content="Load selected outbound",Padding=new Thickness(8),Margin=new Thickness(0,6,0,0)};loadTag.Click+=(_,_)=>{try{ReadFields();}catch(Exception e){MessageBox.Show(this,e.Message);}};details.Children.Add(loadTag);
        Field("Server",address);Field("Port",port);Field("User ID / password",identity);Field("Transport",network);Field("Security · tls / reality / none",security);Field("TLS / Reality server name",sni);
        details.Children.Add(new TextBlock {Text="Settings are saved as a private copy. The JSON tab exposes all protocol and transport options.",TextWrapping=TextWrapping.Wrap,Foreground=AppTheme.Muted,Margin=new Thickness(0,14,0,0)});
        tabs.Items.Add(new TabItem {Header="Connection",Padding=new Thickness(14,8,14,8),Content=new ScrollViewer {Content=details,VerticalScrollBarVisibility=ScrollBarVisibility.Auto}});
        tabs.Items.Add(new TabItem {Header="JSON settings",Padding=new Thickness(14,8,14,8),Content=json});root.Children.Add(tabs);
        tabs.SelectionChanged+=(_,e)=>{if(e.Source!=tabs||updating)return;try{if(previousTab==0)WriteFields();else{config=JsonNode.Parse(json.Text)?.AsObject()??throw new InvalidOperationException("Enter a JSON object.");ReadFields();}previousTab=tabs.SelectedIndex;}catch(Exception error){MessageBox.Show(this,error.Message);updating=true;tabs.SelectedIndex=previousTab;updating=false;}};
        save.Click+=(_,_)=>{try{if(tabs.SelectedIndex==1){config=JsonNode.Parse(json.Text)?.AsObject()??throw new InvalidOperationException("Enter a JSON object.");ReadFields();}else WriteFields();if(!File.Exists(executable.Text))throw new InvalidOperationException("Choose an Xray executable.");if(string.IsNullOrWhiteSpace(name.Text))throw new InvalidOperationException("Enter a configuration name.");Directory.CreateDirectory(Store.DirectoryPath);var file=Path.Combine(Store.DirectoryPath,"xray-"+Guid.NewGuid().ToString("N")+".json");config!["remarks"]=name.Text.Trim();File.WriteAllText(file,config.ToJsonString(Store.Options));Value=new XrayExit(Path.GetFullPath(executable.Text),file,tag.Text.Trim());SavedName=name.Text.Trim();DialogResult=true;}catch(Exception error){MessageBox.Show(this,error.Message,"Xray settings");}};
        if(File.Exists(configPath.Text))TryLoad();
    }
    void TryLoad(){try{config=JsonNode.Parse(File.ReadAllText(configPath.Text))?.AsObject();ReadFields();}catch(Exception e){MessageBox.Show(this,e.Message,"Xray settings");}}
    void ReadFields()
    {
        var outbounds=config?["outbounds"]?.AsArray()??throw new InvalidOperationException("Load an Xray JSON file with outbounds.");
        outbound=outbounds.Select(n=>n!.AsObject()).FirstOrDefault(n=>tag.Text.Length>0?n["tag"]?.GetValue<string>()==tag.Text:n["protocol"]?.GetValue<string>() is not ("freedom" or "blackhole"))??throw new InvalidOperationException("No matching outbound tag.");
        var settings=outbound["settings"]?.AsObject()??throw new InvalidOperationException("Outbound settings are missing.");server=(settings["vnext"]??settings["servers"])?[0]?.AsObject()??throw new InvalidOperationException("This outbound has no editable server.");account=server["users"]?[0]?.AsObject()??server;
        address.Text=server["address"]?.GetValue<string>()??"";port.Text=server["port"]?.ToString()??"443";identity.Text=(account["id"]??account["password"])?.GetValue<string>()??"";var stream=outbound["streamSettings"];network.Text=stream?["network"]?.GetValue<string>()??"tcp";security.Text=stream?["security"]?.GetValue<string>()??"none";sni.Text=(stream?["tlsSettings"]?["serverName"]??stream?["realitySettings"]?["serverName"])?.GetValue<string>()??"";
        if(string.IsNullOrWhiteSpace(name.Text))name.Text=config?["remarks"]?.GetValue<string>()??address.Text;
        tag.Text=outbound["tag"]?.GetValue<string>()??"";json.Text=config!.ToJsonString(Store.Options);
    }
    void WriteFields()
    {
        if(config==null||server==null||outbound==null||account==null)throw new InvalidOperationException("Load a configuration first.");
        if(string.IsNullOrWhiteSpace(address.Text)||!int.TryParse(port.Text,out int number)||number<1||number>65535)throw new InvalidOperationException("Enter a server and port from 1 to 65535.");
        server["address"]=address.Text.Trim();server["port"]=number;var key=account.ContainsKey("id")?"id":"password";if(account.ContainsKey(key)||identity.Text.Length>0)account[key]=identity.Text;outbound["tag"]=tag.Text.Trim();
        var stream=outbound["streamSettings"]?.AsObject();if(stream==null){stream=new JsonObject();outbound["streamSettings"]=stream;}stream["network"]=network.Text.Trim();stream["security"]=security.Text.Trim();string tlsKey=security.Text.Trim()=="reality"?"realitySettings":"tlsSettings";if(sni.Text.Length>0||stream[tlsKey]!=null){var tls=stream[tlsKey]?.AsObject();if(tls==null){tls=new JsonObject();stream[tlsKey]=tls;}tls["serverName"]=sni.Text.Trim();}json.Text=config.ToJsonString(Store.Options);
    }
}

public sealed class TextPrompt : Window
{
    public string Value {get;private set;}="";
    public TextPrompt(string title,string initial){Title=title;Width=420;SizeToContent=SizeToContent.Height;WindowStartupLocation=WindowStartupLocation.CenterOwner;var panel=new StackPanel {Margin=new Thickness(24)};Content=panel;var field=new TextBox {Text=initial,Padding=new Thickness(10)};panel.Children.Add(field);var save=new Button {Content="Rename",IsDefault=true,Padding=new Thickness(12),Margin=new Thickness(0,14,0,0)};save.Click+=(_,_)=>{if(string.IsNullOrWhiteSpace(field.Text))return;Value=field.Text.Trim();DialogResult=true;};panel.Children.Add(save);Loaded+=(_,_)=>{field.Focus();field.SelectAll();};}
}
public sealed class CertificateDialog : Window
{
    readonly CheckBox remember=new(){Content="Remember this fingerprint for this VPN profile"};
    public bool Remember=>remember.IsChecked==true;
    public CertificateDialog(string fingerprint){Title="Gateway certificate";Width=600;SizeToContent=SizeToContent.Height;WindowStartupLocation=WindowStartupLocation.CenterOwner;var panel=new StackPanel {Margin=new Thickness(24)};Content=panel;panel.Children.Add(new TextBlock {Text="The gateway certificate could not be verified. Proceed only if you recognize or independently verified this SHA-256 fingerprint.",TextWrapping=TextWrapping.Wrap,Margin=new Thickness(0,0,0,16)});panel.Children.Add(new TextBox {Text=fingerprint,IsReadOnly=true,TextWrapping=TextWrapping.Wrap,Padding=new Thickness(12),Margin=new Thickness(0,0,0,16)});panel.Children.Add(remember);var actions=new WrapPanel {Margin=new Thickness(0,20,0,0)};var cancel=new Button {Content="Cancel",Padding=new Thickness(16,10,16,10),IsCancel=true};var proceed=new Button {Content="Proceed",Padding=new Thickness(16,10,16,10),Margin=new Thickness(10,0,0,0),Background=AppTheme.Accent,Foreground=AppTheme.Background};proceed.Click+=(_,_)=>DialogResult=true;actions.Children.Add(cancel);actions.Children.Add(proceed);panel.Children.Add(actions);}
}
