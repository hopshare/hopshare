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

* Change the "My Organization" panel of the "My Profile" page as follows:
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


