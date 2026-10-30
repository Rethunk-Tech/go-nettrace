using System.Diagnostics;

namespace Spin;

static class Busy
{
    // Two distinct hot methods so a parser test can check samples land on each by name.
    public static long Alpha(int ms) { var sw = Stopwatch.StartNew(); long n = 0; while (sw.ElapsedMilliseconds < ms) n++; return n; }
    public static long Beta(int ms) { var sw = Stopwatch.StartNew(); long n = 0; while (sw.ElapsedMilliseconds < ms) n += 2; return n; }

    static void Main()
    {
        // The work waits for a line on stdin, so a tracer can attach first however slow the runtime is to start, and
        // the printed result tells it the work is done. The process then stays alive until stdin closes, so the
        // session stops while it runs.
        Console.ReadLine();
        Console.WriteLine(Alpha(600) + Beta(300));
        Console.In.ReadToEnd();
    }
}
