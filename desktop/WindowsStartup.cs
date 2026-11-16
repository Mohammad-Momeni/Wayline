using Microsoft.Win32;

namespace Wayline;

public static class WindowsStartup
{
    const string KeyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";
    public static string Command => "\"" + Environment.ProcessPath + "\" --startup";
    public static bool Enabled
    {
        get
        {
            using var key = Registry.CurrentUser.OpenSubKey(KeyPath);
            return string.Equals(key?.GetValue("Wayline") as string, Command, StringComparison.OrdinalIgnoreCase);
        }
    }
    public static void SetEnabled(bool enabled)
    {
        using var key = Registry.CurrentUser.CreateSubKey(KeyPath);
        if (key.GetValue("Routeweave") is string legacy && legacy.Contains("Routeweave.exe",StringComparison.OrdinalIgnoreCase)) key.DeleteValue("Routeweave",false);
        if (enabled) key.SetValue("Wayline", Command, RegistryValueKind.String);
        else key.DeleteValue("Wayline", false);
    }
}
