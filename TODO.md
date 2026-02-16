# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* It is possible to get 'orphaned' offers to help. If the requesting User deletes your Offer message and doesn't respond, then you never get an answer...is that a problem?
* An Organization Owner can request membership in their own Organization- this should be prevented
* Don't show the "Remove" button on the row for the primary Organization Owner when they go to the Manage Organization page
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.
* Trying to view a private or non-Organization Hop Detail page shows the message "This page is only available to organization owners." - need to parameterize the unauthorized page message?


## Now

* Header

* My Profile
    * Need a way to leave hopShare- "Remove my account" that requires you type in something intentional.
    * Need a way to remove an owned Organization- need to think a bit about this one- to ensure it doesn't get abused.

* Hop Detail Page

* My HopShare Dashboard

* Organizations
    * Need to have a separate set of timebank parameters per organization
        * Minimum balance (default -5)
        * Maximum balance (default 10)
        * Starting balance (default 5)
    * The UI should enforce some sensible levels here to avoid crazy numbers that would make the timebank unusable.

* Joining an Organization should use messages
    * Send an information message to all Owners of an Organization when you request membership. The message body should contain a link that will take the Member directly to their 
    * Send yourself an information message that you requested membership in an Organization.

* Owners are moderators for listings- they can flag/delete inappropriate requests/comments

* Need an Organization-public Member page with more details about each member. Maybe have a way to send them a message?

* Need to create the concept of an Administrator for the application.
    * Admin page is accessed from a special "Admin" menu option in header (only if user is an Administrator). Administrators are set through hopShare's environment via the HOPSHARE_ADMINS variable holding a comma delimited list of usernames (see .env.example). Each hopShare runtime maintains an in-memory service that holds the list of usernames who are Administrators. This keeps things fairly secure- to change the list of Administrators requires privileges to deploy hopShare itself.
    * App overview Tab
        * Answers the question, "What sort of use is the application getting? How are folks using it?"
        * Overall application metrics (number of Organizations, Users, Hops by status, Hours exchanged)
        * Leaderboard- top Organizations by:
            * Total Hops created
            * Total Hours exchanged
            * Total Users
    * Organization "overview" Tab
        * Select an Organization from a searchable list of Organization names.
        * All stats (number of Hops, members, status of each, etc)
        * Actions
            * Delete or Expire Hops
            * Delete specific Hop comments
            * Delete specific Hop images
            * Disable an Organizations
                * We don't delete the Organization as it's possible to re-enable it, so all logic involving Organizations will need to make the check to ensure they aren't disabled
                * When disabled:
                    * Don't show the Organization page anymore
                    * Org should not show up in list of Organizations
                    * Users should not be able to switch to the Organization from My Hopshare
                    * All Users of Organization are logically "removed"
                    * Users who only had that organization are treated as if they don't have an organzation any longer
                * Re-enabling the Organization puts everything back the way it was
    * User overview Tab
        * Select a user from a searchable list of usernames or First/Last names
        * Show what Organizations they belong to, along with all their information, date when they signed up or left, etc.
        * Actions
            * Disable User
            * Delete User completely (GDPR)- produce a page that can be screenshot showing the removal of the user and date/time stamp.
            * Change User's hour balance
    * Message Tab
        * Acts like a "global" messaging system to any User on the application
        * Select a User from a searchable list of usernames or First/Last names
        * Re-use the same 'inbox' that regular Users have
        * Just send 'information' type messages. The message title should be prepended with "ADMIN Message:"
        * Users should be able to reply back to your messages.
Create a plan for how you would add this capability. Assess if there are any security or logical issues with these use cases. Do not write any code just yet.




Change the "My Organization" panel of the "My Profile" page as follows:
* In the list of Organizations the Member is associated with.
    * If the Member is not an Owner of the Organization, follow each Organization row with a placeholder link that says "Leave..." which we will eventually use to let a Member leave that Organization.

## Later

* New User sign ups- need to have them confirm their emails. So we need an email service after signing up.

* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

* Add in basic monitoring (cron job calling script saving in sqlite):
    * net/http/pprof package (visualize performance)
    * runtime.MemStats / runtime.ReadMemStats() thru a /health endpoint on each golang process
    * select count(*) from pg_stat_activity; (database connections)
    * iostat to see iops levels
    * jq against Caddy logs for traffic levels


Font Awesome- https://icon-sets.iconify.design/fa7-regular/page-2.html?keyword=font


