using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Microsoft.Win32;

namespace Wayline;

public sealed class AppPickerDialog : Window
{
    public List<string> Paths {get;private set;}=[];
    readonly HashSet<string> selected;
    readonly SortedDictionary<string,string> apps=new(StringComparer.OrdinalIgnoreCase);
    readonly StackPanel rows=new();
    readonly TextBox search=new(){Padding=new Thickness(10)};
    readonly TextBlock summary=new(){Margin=new Thickness(0,8,0,8),Foreground=AppTheme.Muted};
    [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct ShellFileInfo {public IntPtr icon;public int index;public uint attributes;[MarshalAs(UnmanagedType.ByValTStr,SizeConst=260)] public string display;[MarshalAs(UnmanagedType.ByValTStr,SizeConst=80)] public string type;}
    [DllImport("shell32.dll",CharSet=CharSet.Unicode)] static extern IntPtr SHGetFileInfo(string path,uint attributes,out ShellFileInfo info,uint size,uint flags);
    [DllImport("user32.dll")] static extern bool DestroyIcon(IntPtr icon);
    public AppPickerDialog(IEnumerable<string> current)
    {
        selected=new(current,StringComparer.OrdinalIgnoreCase);Title="Choose VPN apps";Width=700;Height=640;MinWidth=540;MinHeight=420;WindowStartupLocation=WindowStartupLocation.CenterOwner;
        foreach(string path in selected)Add(path);
        foreach(var process in Process.GetProcesses()){using(process)try{if(process.MainModule?.FileName is {} path)Add(path);}catch{}}
        var panel=new DockPanel {Margin=new Thickness(22)};Content=panel;
        var top=new StackPanel();top.Children.Add(new TextBlock {Text="Search running apps or add an executable",FontSize=18,FontWeight=FontWeights.SemiBold,Margin=new Thickness(0,0,0,12)});top.Children.Add(search);top.Children.Add(summary);DockPanel.SetDock(top,Dock.Top);panel.Children.Add(top);
        var actions=new WrapPanel {Margin=new Thickness(0,12,0,0)};var browse=new Button {Content="Browse applications…",Padding=new Thickness(12)};browse.Click+=(_,_)=>{var picker=new OpenFileDialog {Filter="Applications|*.exe|All files|*.*",Multiselect=true};if(picker.ShowDialog(this)==true){foreach(string path in picker.FileNames){Add(path);selected.Add(path);}Render();}};actions.Children.Add(browse);var clear=new Button {Content="Clear selection",Padding=new Thickness(12),Margin=new Thickness(8,0,0,0)};clear.Click+=(_,_)=>{selected.Clear();Render();};actions.Children.Add(clear);var apply=new Button {Content="Apply selection",Padding=new Thickness(12),Margin=new Thickness(8,0,0,0),Background=AppTheme.Accent,Foreground=AppTheme.Background};apply.Click+=(_,_)=>{Paths=selected.Order().ToList();DialogResult=true;};actions.Children.Add(apply);DockPanel.SetDock(actions,Dock.Bottom);panel.Children.Add(actions);
        panel.Children.Add(new ScrollViewer {Content=rows,VerticalScrollBarVisibility=ScrollBarVisibility.Auto});search.TextChanged+=(_,_)=>Render();Render();
    }
    void Add(string path){if(apps.ContainsKey(path))return;string label=System.IO.Path.GetFileNameWithoutExtension(path);try{label=FileVersionInfo.GetVersionInfo(path).FileDescription??label;}catch{}apps[path]=label;}
    void Render()
    {
        rows.Children.Clear();foreach(var item in apps.OrderBy(x=>x.Value)){if(!item.Key.Contains(search.Text,StringComparison.OrdinalIgnoreCase)&&!item.Value.Contains(search.Text,StringComparison.OrdinalIgnoreCase))continue;var row=new DockPanel {Margin=new Thickness(8)};
            try{if(SHGetFileInfo(item.Key,0,out var info,(uint)Marshal.SizeOf<ShellFileInfo>(),0x100|1)!=IntPtr.Zero&&info.icon!=IntPtr.Zero){try{row.Children.Add(new Image {Source=Imaging.CreateBitmapSourceFromHIcon(info.icon,Int32Rect.Empty,BitmapSizeOptions.FromEmptyOptions()),Width=32,Height=32,Margin=new Thickness(0,0,12,0)});}finally{DestroyIcon(info.icon);}}}catch{}
            var labels=new StackPanel();labels.Children.Add(new TextBlock {Text=item.Value,FontWeight=FontWeights.SemiBold});labels.Children.Add(new TextBlock {Text=item.Key,TextWrapping=TextWrapping.Wrap,FontSize=11,Foreground=AppTheme.Muted});row.Children.Add(labels);var check=new CheckBox {Content=row,IsChecked=selected.Contains(item.Key),Margin=new Thickness(0,2,0,2)};check.Click+=(_,_)=>{if(check.IsChecked==true)selected.Add(item.Key);else selected.Remove(item.Key);summary.Text=$"{selected.Count} apps selected";};rows.Children.Add(check);}
        summary.Text=$"{selected.Count} apps selected · {rows.Children.Count} shown";
    }
}
